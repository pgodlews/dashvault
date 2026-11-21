package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func okHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func TestGenerateCSRFToken_UniqueAndHex(t *testing.T) {
	a := generateCSRFToken()
	b := generateCSRFToken()
	if a == "" || b == "" {
		t.Fatal("expected non-empty token")
	}
	if a == b {
		t.Errorf("expected distinct tokens, got %q twice", a)
	}
	if len(a) != 2*csrfTokenBytes {
		t.Errorf("expected %d hex chars, got %d", 2*csrfTokenBytes, len(a))
	}
}

func TestIssueCSRFCookie_NewCookie(t *testing.T) {
	h := issueCSRFCookie(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/cgi-bin/ls.sh?TeslaCam/", nil)
	rec := httptest.NewRecorder()

	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	var found *http.Cookie
	for _, c := range cookies {
		if c.Name == csrfCookieName {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatalf("expected %s cookie in response, got %v", csrfCookieName, cookies)
	}
	if len(found.Value) != 2*csrfTokenBytes {
		t.Errorf("cookie value length: want %d, got %d", 2*csrfTokenBytes, len(found.Value))
	}
	if found.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie SameSite: want Strict, got %v", found.SameSite)
	}
	if found.HttpOnly {
		t.Error("cookie must be HttpOnly=false so JS can read it")
	}
	if found.Path != "/" {
		t.Errorf("cookie Path: want /, got %q", found.Path)
	}
}

func TestIssueCSRFCookie_PreservesExisting(t *testing.T) {
	h := issueCSRFCookie(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/cgi-bin/ls.sh", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "abc123"})
	rec := httptest.NewRecorder()

	h(rec, req)

	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookieName {
			t.Fatalf("should not reissue cookie when one is already present; got %+v", c)
		}
	}
}

func TestRequireCSRF_RejectsWithoutCookie(t *testing.T) {
	h := requireCSRF(okHandler)
	req := httptest.NewRequest(http.MethodPost, "/cgi-bin/reboot.sh", nil)
	req.Header.Set(csrfHeaderName, "sometoken")
	rec := httptest.NewRecorder()

	h(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cookie") {
		t.Errorf("expected body to mention cookie, got %q", rec.Body.String())
	}
}

func TestRequireCSRF_RejectsWithoutHeader(t *testing.T) {
	h := requireCSRF(okHandler)
	req := httptest.NewRequest(http.MethodPost, "/cgi-bin/reboot.sh", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "abc123"})
	rec := httptest.NewRecorder()

	h(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "header") {
		t.Errorf("expected body to mention header, got %q", rec.Body.String())
	}
}

func TestRequireCSRF_RejectsMismatch(t *testing.T) {
	h := requireCSRF(okHandler)
	req := httptest.NewRequest(http.MethodPost, "/cgi-bin/rm.sh?TeslaCam/&foo.mp4", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "cookievalue"})
	req.Header.Set(csrfHeaderName, "differentvalue")
	rec := httptest.NewRecorder()

	h(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRequireCSRF_AcceptsMatch(t *testing.T) {
	h := requireCSRF(okHandler)
	token := "0123456789abcdef"
	req := httptest.NewRequest(http.MethodGet, "/cgi-bin/reboot.sh", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	req.Header.Set(csrfHeaderName, token)
	rec := httptest.NewRecorder()

	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body: want ok, got %q", rec.Body.String())
	}
}

func TestRequireCSRF_BypassedByEnv(t *testing.T) {
	t.Setenv("TESLAUSB_DISABLE_CSRF", "1")
	h := requireCSRF(okHandler)
	req := httptest.NewRequest(http.MethodPost, "/cgi-bin/reboot.sh", nil) // no cookie, no header
	rec := httptest.NewRecorder()

	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("escape hatch not honoured; want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}
