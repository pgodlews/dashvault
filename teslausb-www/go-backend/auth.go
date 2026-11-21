package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type credentialVerifier func(context.Context, string, string) (bool, error)

var webUIAuthFile = "/etc/nginx/auth/.htpasswd"

// verifyWebUICredentials uses the same password file as nginx. apache2-utils
// supports the existing APR1 hashes without introducing a second credential
// store. Pass the password on stdin, never in process arguments or logs.
func verifyWebUICredentials(ctx context.Context, user, password string) (bool, error) {
	if strings.HasPrefix(user, "-") {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "htpasswd", "-iv", webUIAuthFile, user)
	cmd.Stdin = strings.NewReader(password + "\n")
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && (exitErr.ExitCode() == 3 || exitErr.ExitCode() == 6) {
		return false, nil // credential mismatch or unknown user
	}
	return false, err // missing file/tool, timeout, or other verifier failure
}

// verifiedCredentialTTL bounds how long a correct login is remembered.
const verifiedCredentialTTL = 5 * time.Minute

// cachedVerifier remembers the last correct login so that not every request
// starts an htpasswd process. nginx asks the backend about every request it
// serves (see authGate), which makes the check hot on a Pi Zero.
//
// Only a success is remembered, as a hash, for verifiedCredentialTTL, and only
// while the password file is unchanged. A wrong login is always verified again.
func cachedVerifier(verify credentialVerifier, authFile func() string) credentialVerifier {
	var (
		mu      sync.Mutex
		digest  [sha256.Size]byte
		stamp   string
		expires time.Time
	)
	fileStamp := func() string {
		info, err := os.Stat(authFile())
		if err != nil {
			return ""
		}
		return info.ModTime().UTC().Format(time.RFC3339Nano) + "/" + strconv.FormatInt(info.Size(), 10)
	}
	return func(ctx context.Context, user, password string) (bool, error) {
		sum := sha256.Sum256([]byte(user + "\x00" + password))
		current := fileStamp()
		if current != "" {
			mu.Lock()
			hit := stamp == current && time.Now().Before(expires) && subtle.ConstantTimeCompare(sum[:], digest[:]) == 1
			mu.Unlock()
			if hit {
				return true, nil
			}
		}
		valid, err := verify(ctx, user, password)
		if err == nil && valid && current != "" {
			mu.Lock()
			digest, stamp, expires = sum, current, time.Now().Add(verifiedCredentialTTL)
			mu.Unlock()
		}
		return valid, err
	}
}

// authGatePath is the nginx auth_request target (see teslausb.nginx).
const authGatePath = "/api/auth/check"

// authGate answers nginx's per-request question "may this client in?".
// nginx's own auth_basic keeps no count of failures, so with it an attacker
// could guess passwords against any static file without limit. nginx therefore
// leaves the decision to this handler for every request it serves:
//
//	204  credentials are correct
//	401  credentials are missing or wrong
//	403  this address is locked out after repeated wrong passwords; the
//	     answer is the same whether or not the password sent now is correct
//
// A request without credentials is not a guess and is not counted: browsers
// send one before they prompt, and fetch some files without credentials.
func authGate(verify credentialVerifier) http.HandlerFunc {
	check := requireBasicAuth(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}, verify)
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		limiter := globalRateLimiter
		if limiter.isLocked(ip) {
			http.Error(w, "Too many failed login attempts — try again later", http.StatusForbidden)
			return
		}
		lw := &statusCapture{ResponseWriter: w, code: http.StatusOK}
		check(lw, r)
		switch {
		case lw.code == http.StatusUnauthorized && r.Header.Get("Authorization") != "":
			limiter.recordFailure(ip)
		case lw.code < 400:
			limiter.recordSuccess(ip)
		}
	}
}

func requireBasicAuth(next http.HandlerFunc, verify credentialVerifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		// htpasswd reads a single line. Reject control characters, leading-dash
		// usernames, and oversized credentials before invoking the verifier.
		if !ok || user == "" || password == "" || len(user) > 256 || len(password) > 4096 ||
			strings.HasPrefix(user, "-") || strings.ContainsAny(user+password, "\r\n\x00") || strings.Contains(user, ":") {
			basicAuthChallenge(w)
			return
		}
		valid, err := verify(r.Context(), user, password)
		if err != nil {
			http.Error(w, "Authentication service unavailable", http.StatusServiceUnavailable)
			return
		}
		if !valid {
			basicAuthChallenge(w)
			return
		}
		next(w, r)
	}
}

func basicAuthChallenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="teslausb"`)
	http.Error(w, "Authentication required", http.StatusUnauthorized)
}
