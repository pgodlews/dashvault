package main

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Discover the actual registration sites so future routes join this check.
// Requests are served in-memory; handlers must not reach filesystem, hardware
// or network operations when authentication is missing or incorrect.
func TestAllRoutesRequireAuthentication(t *testing.T) {
	t.Setenv("TESLAUSB_DISABLE_CSRF", "1") // auth remains mandatory for legacy clients
	f, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	h := newHTTPHandler(NewBroker(nil, time.Second), func(context.Context, string, string) (bool, error) {
		return false, nil
	})
	count := 0
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "HandleFunc" {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok {
			t.Fatal("route must be a literal for coverage")
		}
		pattern, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.Fields(pattern)
		method, path := http.MethodGet, parts[len(parts)-1]
		if len(parts) == 2 {
			method = parts[0]
		}
		path = strings.ReplaceAll(path, "{ssid}", "placeholder")
		count++
		t.Run(pattern, func(t *testing.T) {
			for _, badCredentials := range []bool{false, true} {
				r := httptest.NewRequest(method, path, nil)
				if badCredentials {
					r.SetBasicAuth("test-user", "test-password")
				}
				// Spoofed proxy identity must not grant access.
				r.Header.Set("X-Auth-Status", "200")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
					t.Fatalf("authentication not enforced: status %d", w.Code)
				}
				// Avoid exercising lockout here; every route must reach auth.
				globalRateLimiter.recordSuccess(clientIP(r))
			}
		})
		return true
	})
	if count == 0 {
		t.Fatal("no routes audited")
	}
	t.Logf("audited %d route registrations", count)
}

func TestBasicAuthFailureModes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		user     string
		password string
		valid    bool
		err      error
		want     int
	}{
		{"valid", "test-user", "test-password", true, nil, http.StatusNoContent},
		{"mismatch", "test-user", "test-password", false, nil, http.StatusUnauthorized},
		{"unavailable", "test-user", "test-password", false, errors.New("unavailable"), http.StatusServiceUnavailable},
		{"newline", "test-user", "test-password\nextra", true, nil, http.StatusUnauthorized},
		{"empty", "test-user", "", true, nil, http.StatusUnauthorized},
		{"dash-user", "-n", "test-password", true, nil, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := requireBasicAuth(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}, func(context.Context, string, string) (bool, error) {
				return tc.valid, tc.err
			})
			r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
			r.SetBasicAuth(tc.user, tc.password)
			w := httptest.NewRecorder()
			h(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestHtpasswdVerification(t *testing.T) {
	if _, err := exec.LookPath("htpasswd"); err != nil {
		t.Skip("htpasswd unavailable")
	}
	old := webUIAuthFile
	webUIAuthFile = filepath.Join(t.TempDir(), "credentials")
	t.Cleanup(func() { webUIAuthFile = old })
	// Use stdin here too; never put even fixture passwords in process arguments.
	cmd := exec.Command("htpasswd", "-icm", webUIAuthFile, "test-user")
	cmd.Stdin = strings.NewReader("test-password\n")
	if err := cmd.Run(); err != nil {
		t.Fatal("could not generate fixture credentials")
	}
	for _, tc := range []struct {
		user, password string
		want           bool
	}{
		{"test-user", "test-password", true},
		{"test-user", "incorrect", false},
		{"unknown-user", "test-password", false},
	} {
		ok, err := verifyWebUICredentials(context.Background(), tc.user, tc.password)
		if err != nil || ok != tc.want {
			t.Fatalf("verification mismatch: valid=%v, error=%v", ok, err != nil)
		}
	}
	if err := os.Remove(webUIAuthFile); err != nil {
		t.Fatal(err)
	}
	if ok, err := verifyWebUICredentials(context.Background(), "test-user", "test-password"); ok || err == nil {
		t.Fatal("missing credentials must fail closed")
	}
}

func TestAuthenticatedRoutesKeepCSRFAndStreaming(t *testing.T) {
	t.Setenv("TESLAUSB_DISABLE_CSRF", "")
	h := newHTTPHandler(NewBroker(nil, time.Second), func(context.Context, string, string) (bool, error) {
		return true, nil
	})
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/api/status", http.StatusOK},
		{http.MethodPost, "/cgi-bin/reboot.sh", http.StatusForbidden},
		{http.MethodGet, "/api/status/stream", http.StatusOK},
	} {
		t.Run(tc.path, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // SSE sends its initial event, then exits without a socket.
			r := httptest.NewRequest(tc.method, tc.path, nil).WithContext(ctx)
			r.SetBasicAuth("test-user", "test-password")
			w := httptest.NewRecorder()
			h(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d, want %d", w.Code, tc.want)
			}
			if tc.path == "/api/status" && len(w.Result().Cookies()) == 0 {
				t.Fatal("missing CSRF cookie")
			}
			if tc.path == "/api/status/stream" && !strings.Contains(w.Body.String(), "data:") {
				t.Fatal("missing initial SSE snapshot")
			}
		})
	}
}
