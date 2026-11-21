package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// setupDocRoot points DOCUMENT_ROOT at a temp dir with a TeslaCam subdir.
func setupDocRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "TeslaCam"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCUMENT_ROOT", root)
	return root
}

func TestHandleMv_success(t *testing.T) {
	root := setupDocRoot(t)
	src := filepath.Join(root, "TeslaCam", "a.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/mv?TeslaCam/&a.txt&b.txt", nil)
	w := httptest.NewRecorder()
	HandleMv(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(root, "TeslaCam", "b.txt")); err != nil {
		t.Errorf("dest missing after move: %v", err)
	}
}

func TestHandleMv_missingSourceReturns500(t *testing.T) {
	setupDocRoot(t)

	req := httptest.NewRequest(http.MethodGet, "/mv?TeslaCam/&nope.txt&b.txt", nil)
	w := httptest.NewRecorder()
	HandleMv(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d: %s", w.Code, w.Body)
	}
}

func TestHandleCp_missingSourceReturns500(t *testing.T) {
	setupDocRoot(t)

	req := httptest.NewRequest(http.MethodGet, "/cp?TeslaCam/&nope.txt&b.txt", nil)
	w := httptest.NewRecorder()
	HandleCp(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d: %s", w.Code, w.Body)
	}
}

func TestHandleMkdir_failureReturns500(t *testing.T) {
	root := setupDocRoot(t)
	// A regular file in the way makes MkdirAll fail.
	if err := os.WriteFile(filepath.Join(root, "TeslaCam", "blocker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/mkdir?TeslaCam/&blocker/sub", nil)
	w := httptest.NewRecorder()
	HandleMkdir(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d: %s", w.Code, w.Body)
	}
}

func TestHandleMkdir_success(t *testing.T) {
	root := setupDocRoot(t)

	req := httptest.NewRequest(http.MethodGet, "/mkdir?TeslaCam/&newdir", nil)
	w := httptest.NewRecorder()
	HandleMkdir(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if info, err := os.Stat(filepath.Join(root, "TeslaCam", "newdir")); err != nil || !info.IsDir() {
		t.Errorf("dir not created: %v", err)
	}
}

func TestHandleDownloadZip_success(t *testing.T) {
	root := setupDocRoot(t)
	if err := os.WriteFile(filepath.Join(root, "TeslaCam", "clip.mp4"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/zip?TeslaCam", nil)
	w := httptest.NewRecorder()
	HandleDownloadZip(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if w.Body.Len() == 0 {
		t.Error("expected non-empty zip body")
	}
}

func TestHandleDownloadZip_missingDirReturns500(t *testing.T) {
	setupDocRoot(t)

	req := httptest.NewRequest(http.MethodGet, "/zip?TeslaCam%2Fnope", nil)
	w := httptest.NewRecorder()
	HandleDownloadZip(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d: %s", w.Code, w.Body)
	}
}
