package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"
)

const (
	csrfCookieName = "teslausb_csrf"
	csrfHeaderName = "X-CSRF-Token"
	csrfTokenBytes = 32
)

// csrfDisabled reports whether the TESLAUSB_DISABLE_CSRF escape hatch is set.
// Documented as a compatibility knob for third-party clients that predate the
// header-based CSRF scheme; do not rely on it in production.
func csrfDisabled() bool {
	return os.Getenv("TESLAUSB_DISABLE_CSRF") == "1"
}

// generateCSRFToken returns a cryptographically random hex token, or empty on
// RNG failure (caller must treat "" as a hard error).
func generateCSRFToken() string {
	b := make([]byte, csrfTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// ensureCSRFCookie returns the token on the request's teslausb_csrf cookie, or
// mints a new one and writes it to the response. The cookie is HttpOnly=false
// so the UI JS can read it and echo it as X-CSRF-Token on mutating fetches.
// SameSite=Strict prevents cross-site fetches from ever carrying it.
func ensureCSRFCookie(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(csrfCookieName); err == nil && c.Value != "" {
		return c.Value
	}
	token := generateCSRFToken()
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: false,
		SameSite: http.SameSiteStrictMode,
	})
	return token
}

// issueCSRFCookie wraps a read-only handler so its response sets the CSRF
// cookie when the client doesn't yet have one. No enforcement.
func issueCSRFCookie(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ensureCSRFCookie(w, r)
		next(w, r)
	}
}

// requireCSRF wraps a mutating handler. The request must present a matching
// X-CSRF-Token header and teslausb_csrf cookie, compared in constant time.
// Browsers cannot attach arbitrary headers from <img>/<form>/cross-origin
// fetch, so this alone blocks classic CSRF attacks regardless of the HTTP
// method used by the legacy UI (it issues GETs for most mutating ops).
// Bypassed entirely when TESLAUSB_DISABLE_CSRF=1 — compat escape hatch only.
func requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if csrfDisabled() {
			next(w, r)
			return
		}
		cookie, err := r.Cookie(csrfCookieName)
		if err != nil || cookie.Value == "" {
			http.Error(w, "CSRF cookie missing", http.StatusForbidden)
			return
		}
		header := r.Header.Get(csrfHeaderName)
		if header == "" {
			http.Error(w, "CSRF header missing", http.StatusForbidden)
			return
		}
		if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
			http.Error(w, "CSRF token mismatch", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}
