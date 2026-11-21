package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setDocRoot points DOCUMENT_ROOT at a tmpdir for the duration of the test and
// returns the path. HandleUpload writes to DOCUMENT_ROOT + args[0] + args[1].
func setDocRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DOCUMENT_ROOT", dir)
	return dir
}

func TestUploadLimitBytes_Default(t *testing.T) {
	t.Setenv("TESLAUSB_MAX_UPLOAD_MB", "")
	if got := uploadLimitBytes(); got != int64(defaultUploadLimitMB)*1024*1024 {
		t.Errorf("default upload limit: got %d, want %d", got, int64(defaultUploadLimitMB)*1024*1024)
	}
}

func TestUploadLimitBytes_EnvOverride(t *testing.T) {
	t.Setenv("TESLAUSB_MAX_UPLOAD_MB", "50")
	if got := uploadLimitBytes(); got != 50*1024*1024 {
		t.Errorf("env override: got %d, want %d", got, 50*1024*1024)
	}
}

func TestUploadLimitBytes_EnvIgnoredWhenInvalid(t *testing.T) {
	t.Setenv("TESLAUSB_MAX_UPLOAD_MB", "not-a-number")
	if got := uploadLimitBytes(); got != int64(defaultUploadLimitMB)*1024*1024 {
		t.Errorf("invalid env: got %d, want default", got)
	}
	t.Setenv("TESLAUSB_MAX_UPLOAD_MB", "0")
	if got := uploadLimitBytes(); got != int64(defaultUploadLimitMB)*1024*1024 {
		t.Errorf("zero env: got %d, want default", got)
	}
}

func TestHandleUpload_UnderLimit(t *testing.T) {
	dir := setDocRoot(t)
	// The handler parses query args joined by &: first = base dir, second = relpath.
	req := httptest.NewRequest(http.MethodPost, "/cgi-bin/upload.sh?uploads&hello.txt", strings.NewReader("hello world"))
	rec := httptest.NewRecorder()

	t.Setenv("TESLAUSB_MAX_UPLOAD_MB", "1")
	// ensure target directory exists; GetSafePath only validates, it does not mkdir.
	if err := os.MkdirAll(filepath.Join(dir, "uploads"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	HandleUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "uploads", "hello.txt"))
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("content: want %q, got %q", "hello world", string(got))
	}
}

func TestHandleUpload_OverLimitReturns413(t *testing.T) {
	dir := setDocRoot(t)
	// 2 MiB body, limit set to 1 MiB.
	body := bytes.Repeat([]byte("x"), 2*1024*1024)
	req := httptest.NewRequest(http.MethodPost, "/cgi-bin/upload.sh?uploads&big.bin", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	t.Setenv("TESLAUSB_MAX_UPLOAD_MB", "1")
	if err := os.MkdirAll(filepath.Join(dir, "uploads"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	HandleUpload(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: want 413, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: want application/json, got %q", ct)
	}
	var payload map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if !strings.Contains(payload["error"], "limit") {
		t.Errorf("error body: want mention of limit, got %q", payload["error"])
	}
	// The partial file must be cleaned up on overflow.
	if _, err := os.Stat(filepath.Join(dir, "uploads", "big.bin")); !os.IsNotExist(err) {
		t.Errorf("partial file not cleaned up: err=%v", err)
	}
}

// TestHandleUpload_OverwriteIsAtomic verifies that an overwrite-upload that
// fails mid-stream leaves the previous file content intact (no truncated mix
// of old and new bytes). Uses the body-size cap to force a mid-stream error.
func TestHandleUpload_OverwriteIsAtomic(t *testing.T) {
	dir := setDocRoot(t)
	target := filepath.Join(dir, "uploads", "atomic.txt")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := []byte("ORIGINAL CONTENTS")
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	t.Setenv("TESLAUSB_MAX_UPLOAD_MB", "1")
	body := bytes.Repeat([]byte("y"), 2*1024*1024) // 2 MiB > 1 MiB limit
	req := httptest.NewRequest(http.MethodPost, "/cgi-bin/upload.sh?uploads&atomic.txt", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleUpload(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: want 413, got %d", rec.Code)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target after failed upload: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Errorf("destination was clobbered by failed upload: got %q", got)
	}
}

// TestGetSafePath_RejectsSiblingDirPrefix exercises the prefix-string trap.
// With docRoot=/tmp/.../html, a request for "../html-evil/x" must be rejected.
func TestGetSafePath_RejectsSiblingDirPrefix(t *testing.T) {
	tmp := t.TempDir()
	docRoot := filepath.Join(tmp, "html")
	if err := os.MkdirAll(filepath.Join(tmp, "html-evil"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(docRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("DOCUMENT_ROOT", docRoot)

	if _, err := GetSafePath([]string{"../html-evil", "anything"}); err == nil {
		t.Errorf("GetSafePath should reject sibling-prefix escape")
	}
}
