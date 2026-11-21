package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestLimiter returns a fresh rateLimiter with no gc goroutine (tests manage state directly).
func newTestLimiter() *rateLimiter {
	return &rateLimiter{
		attempts: make(map[string][]time.Time),
		locked:   make(map[string]time.Time),
	}
}

func TestRateLimiter_NotLockedInitially(t *testing.T) {
	rl := newTestLimiter()
	if rl.isLocked("1.2.3.4") {
		t.Fatal("fresh IP should not be locked")
	}
}

func TestRateLimiter_LockoutAfterThreshold(t *testing.T) {
	t.Setenv("TESLAUSB_RATE_LIMIT_ATTEMPTS", "3")
	t.Setenv("TESLAUSB_RATE_LIMIT_WINDOW", "60")
	t.Setenv("TESLAUSB_RATE_LIMIT_LOCKOUT", "300")

	rl := newTestLimiter()
	ip := "10.0.0.1"

	rl.recordFailure(ip)
	rl.recordFailure(ip)
	if rl.isLocked(ip) {
		t.Fatal("should not be locked after 2 failures (threshold=3)")
	}

	rl.recordFailure(ip) // 3rd — triggers lockout
	if !rl.isLocked(ip) {
		t.Fatal("should be locked after 3 failures")
	}
}

func TestRateLimiter_SuccessClearsHistory(t *testing.T) {
	t.Setenv("TESLAUSB_RATE_LIMIT_ATTEMPTS", "3")
	t.Setenv("TESLAUSB_RATE_LIMIT_WINDOW", "60")
	t.Setenv("TESLAUSB_RATE_LIMIT_LOCKOUT", "300")

	rl := newTestLimiter()
	ip := "10.0.0.2"

	rl.recordFailure(ip)
	rl.recordFailure(ip)
	rl.recordSuccess(ip) // reset
	rl.recordFailure(ip) // counter restarted — still only 1
	if rl.isLocked(ip) {
		t.Fatal("success should have cleared the failure counter")
	}
}

func TestRateLimiter_ExpiredLockoutClears(t *testing.T) {
	rl := newTestLimiter()
	ip := "10.0.0.3"
	// Manually plant an already-expired lockout.
	rl.locked[ip] = time.Now().Add(-1 * time.Second)
	if rl.isLocked(ip) {
		t.Fatal("expired lockout should be cleared on check")
	}
}

func TestRateLimiter_WindowPrunesOldFailures(t *testing.T) {
	t.Setenv("TESLAUSB_RATE_LIMIT_ATTEMPTS", "3")
	t.Setenv("TESLAUSB_RATE_LIMIT_WINDOW", "1") // 1-second window
	t.Setenv("TESLAUSB_RATE_LIMIT_LOCKOUT", "300")

	rl := newTestLimiter()
	ip := "10.0.0.4"

	// Plant two old failures just outside the window.
	old := time.Now().Add(-2 * time.Second)
	rl.attempts[ip] = []time.Time{old, old}

	rl.recordFailure(ip) // only 1 within window — no lockout
	if rl.isLocked(ip) {
		t.Fatal("old failures outside window should not contribute to lockout")
	}
}

func TestAuthRateLimit_Blocks429WhenLocked(t *testing.T) {
	// Swap in a test limiter with the IP pre-locked.
	orig := globalRateLimiter
	defer func() { globalRateLimiter = orig }()

	tl := newTestLimiter()
	tl.locked["192.168.1.1"] = time.Now().Add(5 * time.Minute)
	globalRateLimiter = tl

	h := authRateLimit(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/cgi-bin/ls.sh", nil)
	req.Header.Set("X-Real-IP", "192.168.1.1")
	rec := httptest.NewRecorder()

	h(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d", rec.Code)
	}
}

func TestAuthRateLimit_RecordsFailureOn401(t *testing.T) {
	orig := globalRateLimiter
	defer func() { globalRateLimiter = orig }()
	globalRateLimiter = newTestLimiter()

	t.Setenv("TESLAUSB_RATE_LIMIT_ATTEMPTS", "2")
	t.Setenv("TESLAUSB_RATE_LIMIT_WINDOW", "60")
	t.Setenv("TESLAUSB_RATE_LIMIT_LOCKOUT", "300")

	h := authRateLimit(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	ip := "172.16.0.1"
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/cgi-bin/ls.sh", nil)
		req.Header.Set("X-Real-IP", ip)
		rec := httptest.NewRecorder()
		h(rec, req)
	}

	if !globalRateLimiter.isLocked(ip) {
		t.Fatal("IP should be locked after 2 × 401")
	}
}

func TestAuthRateLimit_ClearsOnSuccess(t *testing.T) {
	orig := globalRateLimiter
	defer func() { globalRateLimiter = orig }()
	globalRateLimiter = newTestLimiter()

	t.Setenv("TESLAUSB_RATE_LIMIT_ATTEMPTS", "5")
	t.Setenv("TESLAUSB_RATE_LIMIT_WINDOW", "60")
	t.Setenv("TESLAUSB_RATE_LIMIT_LOCKOUT", "300")

	ip := "172.16.0.2"
	// Prime with 3 failures.
	globalRateLimiter.recordFailure(ip)
	globalRateLimiter.recordFailure(ip)
	globalRateLimiter.recordFailure(ip)

	// A successful response should reset.
	h := authRateLimit(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Real-IP", ip)
	h(httptest.NewRecorder(), req)

	if globalRateLimiter.isLocked(ip) {
		t.Fatal("should not be locked after success")
	}
	if len(globalRateLimiter.attempts[ip]) != 0 {
		t.Fatal("failure history should be cleared after success")
	}
}

func TestClientIP_PrefersXRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Real-IP", "1.2.3.4")
	req.RemoteAddr = "127.0.0.1:12345"
	if got := clientIP(req); got != "1.2.3.4" {
		t.Errorf("want 1.2.3.4, got %q", got)
	}
}

func TestClientIP_FallsBackToRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:54321"
	if got := clientIP(req); got != "10.0.0.5" {
		t.Errorf("want 10.0.0.5, got %q", got)
	}
}
