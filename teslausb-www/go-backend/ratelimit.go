package main

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// rateLimiter tracks failed basic-auth attempts per client IP and temporarily
// locks out IPs that exceed the threshold. It is intentionally simple: state
// lives in memory and is lost on restart, which is acceptable — a reboot
// resets the lockout and is itself a legitimate recovery path.
//
// Defaults (all overridable via environment variables):
//
//	TESLAUSB_RATE_LIMIT_ATTEMPTS  — max failures before lockout (default 10)
//	TESLAUSB_RATE_LIMIT_WINDOW    — rolling window in seconds   (default 60)
//	TESLAUSB_RATE_LIMIT_LOCKOUT   — lockout duration in seconds (default 300)
type rateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time // IP → timestamps of recent failures
	locked   map[string]time.Time   // IP → lockout expiry
}

func newRateLimiter() *rateLimiter {
	rl := &rateLimiter{
		attempts: make(map[string][]time.Time),
		locked:   make(map[string]time.Time),
	}
	go rl.gc() // background sweep to keep maps from growing unbounded
	return rl
}

var globalRateLimiter = newRateLimiter()

// rateLimitConfig reads the three tunables from env, falling back to defaults.
func rateLimitConfig() (maxAttempts int, window time.Duration, lockout time.Duration) {
	maxAttempts = intEnv("TESLAUSB_RATE_LIMIT_ATTEMPTS", 10)
	window = time.Duration(intEnv("TESLAUSB_RATE_LIMIT_WINDOW", 60)) * time.Second
	lockout = time.Duration(intEnv("TESLAUSB_RATE_LIMIT_LOCKOUT", 300)) * time.Second
	return
}

func intEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// isLocked returns true if the IP is currently locked out.
func (rl *rateLimiter) isLocked(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	expiry, ok := rl.locked[ip]
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		delete(rl.locked, ip)
		return false
	}
	return true
}

// recordFailure records a failed auth attempt and locks out the IP if the
// threshold is exceeded within the rolling window.
func (rl *rateLimiter) recordFailure(ip string) {
	maxAttempts, window, lockout := rateLimitConfig()
	now := time.Now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Prune attempts outside the window.
	cutoff := now.Add(-window)
	recent := rl.attempts[ip][:0]
	for _, t := range rl.attempts[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	recent = append(recent, now)
	rl.attempts[ip] = recent

	if len(recent) >= maxAttempts {
		rl.locked[ip] = now.Add(lockout)
		delete(rl.attempts, ip) // reset counter after lockout
	}
}

// recordSuccess clears the failure history for an IP on successful auth.
func (rl *rateLimiter) recordSuccess(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.attempts, ip)
	delete(rl.locked, ip)
}

// gc sweeps expired entries every 5 minutes.
func (rl *rateLimiter) gc() {
	for range time.Tick(5 * time.Minute) {
		_, window, _ := rateLimitConfig()
		now := time.Now()
		cutoff := now.Add(-window)

		rl.mu.Lock()
		for ip, times := range rl.attempts {
			fresh := times[:0]
			for _, t := range times {
				if t.After(cutoff) {
					fresh = append(fresh, t)
				}
			}
			if len(fresh) == 0 {
				delete(rl.attempts, ip)
			} else {
				rl.attempts[ip] = fresh
			}
		}
		for ip, expiry := range rl.locked {
			if now.After(expiry) {
				delete(rl.locked, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// authRateLimit wraps a handler and enforces the lockout policy.
// It observes backend credential verification: 401 records a failure and a
// successful response clears prior failures. nginx also verifies credentials,
// so attempts rejected at nginx never reach this limiter.
func authRateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)

		if globalRateLimiter.isLocked(ip) {
			http.Error(w, "Too many failed login attempts — try again later", http.StatusTooManyRequests)
			return
		}

		// Wrap the ResponseWriter to capture the status code.
		lw := &statusCapture{ResponseWriter: w, code: http.StatusOK}
		next(lw, r)

		if lw.code == http.StatusUnauthorized {
			globalRateLimiter.recordFailure(ip)
		} else if lw.code < 400 {
			globalRateLimiter.recordSuccess(ip)
		}
	}
}

// statusCapture captures the status code written by the handler.
type statusCapture struct {
	http.ResponseWriter
	code    int
	written bool
}

func (s *statusCapture) WriteHeader(code int) {
	if !s.written {
		s.code = code
		s.written = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusCapture) Write(b []byte) (int, error) {
	if !s.written {
		s.code = http.StatusOK
		s.written = true
	}
	return s.ResponseWriter.Write(b)
}

// Flush forwards to the underlying ResponseWriter if it supports http.Flusher,
// so SSE handlers work correctly through this middleware wrapper.
func (s *statusCapture) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// clientIP extracts the real client IP, preferring X-Real-IP set by nginx.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
