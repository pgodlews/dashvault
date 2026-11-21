package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	auditLogPath = "/mutable/audit.log"
	logFileMode  = 0640
)

var auditMu sync.Mutex

// AuditEntry is a structured log line written for every mutating endpoint
// invocation. Timestamp + source IP are always present; User is populated from
// the basic-auth credentials (decoded, not verified — nginx already enforced it).
type AuditEntry struct {
	Time     string `json:"time"`
	Method   string `json:"method"`
	Path     string `json:"path"`           // clean path (no query)
	Args     string `json:"args,omitempty"` // RawQuery redacted of passwords/tokens
	Source   string `json:"source_ip"`      // RemoteAddr hostname or IP
	User     string `json:"user,omitempty"` // basic-auth username (from Authorization header)
	Status   int    `json:"status"`         // HTTP status code returned to client
	Duration string `json:"duration_ms"`    // request processing time in ms
}

// auditWriter wraps an http.ResponseWriter so we capture the status code (required for AuditEntry).
type auditResponseWriter struct {
	http.ResponseWriter
	code int
	path string
}

func newAuditWriter(w http.ResponseWriter, path string) *auditResponseWriter {
	return &auditResponseWriter{w, http.StatusOK, path}
}

func (a *auditResponseWriter) WriteHeader(code int) {
	a.code = code
	a.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying ResponseWriter if it supports http.Flusher,
// so SSE handlers work correctly through this middleware wrapper.
func (a *auditResponseWriter) Flush() {
	if f, ok := a.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// auditMiddleware wraps a handler and emits a JSON log line to both stderr (so
// journald picks it up when the service runs under systemd) and an append-only
// file at auditLogPath. Non-mutating (read-only) handlers skip this — only the
// mutating endpoints pass through here.
func auditMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		aw := newAuditWriter(w, r.URL.Path)
		next(aw, r)

		duration := time.Since(start).Milliseconds()

		entry := AuditEntry{
			Time:     time.Now().UTC().Format(time.RFC3339),
			Method:   r.Method,
			Path:     aw.path,
			Args:     redactSecrets(r.URL.RawQuery),
			Source:   r.RemoteAddr,
			User:     extractBasicAuthUser(r),
			Status:   aw.code,
			Duration: fmt.Sprintf("%d", duration),
		}

		logAudit(entry)
	}
}

// logAudit writes the entry as a single JSON line. Stderr feeds journald when
// teslausb-backend runs under systemd; the same line is appended to the disk file.
func logAudit(entry AuditEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		return // never allow JSON marshalling failures to affect the response path
	}

	// Always log to stderr (→ journald)
	fmt.Fprintln(os.Stderr, string(data))

	// Append to persistent audit file (best-effort — Pi SD cards can fill up).
	go func() {
		auditMu.Lock()
		defer auditMu.Unlock()
		f, err := os.OpenFile(auditLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, logFileMode)
		if err != nil {
			return
		}
		defer f.Close()
		fmt.Fprintln(f, string(data))
	}()
}

// extractBasicAuthUser decodes the base64 username from the Authorization: Basic header.
func extractBasicAuthUser(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Basic ") {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(auth[6:])
	if err != nil {
		return ""
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return ""
	}
	return parts[0]
}

// redactSecrets removes query params that look like passwords or tokens so they
// never end up in audit logs. The blacklist is conservative — we redact to be safe.
func redactSecrets(raw string) string {
	if raw == "" {
		return ""
	}
	out, _ := urlQueryRedact(raw)
	return out
}

func urlQueryRedact(raw string) (string, error) {
	// Simple split-on-`&` approach to avoid importing url.QueryUnescape here.
	pairs := strings.Split(raw, "&")
	for i, p := range pairs {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) == 2 {
			lower := strings.ToLower(kv[0])
			if lower == "password" || lower == "token" || lower == "api_key" {
				pairs[i] = kv[0] + "=***redacted***"
			}
		}
	}
	return strings.Join(pairs, "&"), nil
}
