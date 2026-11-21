package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConf(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "teslausb_setup_variables.conf")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// stubRemount replaces the mount syscall used by withConfRW with a no-op so
// tests can exercise the conf-write paths without root privileges.
func stubRemount(t *testing.T) {
	t.Helper()
	orig := remountRoot
	originalLockPath := rootFSLockPath
	remountRoot = func(string) error { return nil }
	rootFSLockPath = filepath.Join(t.TempDir(), "rootfs.lock")
	t.Cleanup(func() {
		remountRoot = orig
		rootFSLockPath = originalLockPath
	})
}

func TestReadSnapshotInterval_default(t *testing.T) {
	// Point at a non-existent file — should return the default.
	origConf := confFilePath
	confFilePath = "/nonexistent/path/conf"
	defer func() { confFilePath = origConf }()

	got := readSnapshotInterval()
	if got != defaultSnapshotInterval {
		t.Errorf("want %d, got %d", defaultSnapshotInterval, got)
	}
}

func TestReadSnapshotInterval_fromFile(t *testing.T) {
	p := writeConf(t, "export FOO=bar\nexport SNAPSHOT_INTERVAL=300\n")
	origConf := confFilePath
	confFilePath = p
	defer func() { confFilePath = origConf }()

	got := readSnapshotInterval()
	if got != 300 {
		t.Errorf("want 300, got %d", got)
	}
}

func TestWriteSnapshotInterval_upsert(t *testing.T) {
	stubRemount(t)
	p := writeConf(t, "export FOO=bar\n# export SNAPSHOT_INTERVAL=480\n")
	origConf := confFilePath
	confFilePath = p
	defer func() { confFilePath = origConf }()

	if err := writeSnapshotInterval(120); err != nil {
		t.Fatal(err)
	}
	got := readSnapshotInterval()
	if got != 120 {
		t.Errorf("want 120, got %d", got)
	}
}

func TestWriteSnapshotInterval_append(t *testing.T) {
	stubRemount(t)
	p := writeConf(t, "export FOO=bar\n")
	origConf := confFilePath
	confFilePath = p
	defer func() { confFilePath = origConf }()

	if err := writeSnapshotInterval(900); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "export SNAPSHOT_INTERVAL=900") {
		t.Errorf("expected SNAPSHOT_INTERVAL=900 in conf, got:\n%s", data)
	}
}

func TestHandleSnapshotInterval_get(t *testing.T) {
	p := writeConf(t, fmt.Sprintf("export SNAPSHOT_INTERVAL=%d\n", 600))
	origConf := confFilePath
	confFilePath = p
	defer func() { confFilePath = origConf }()

	req := httptest.NewRequest(http.MethodGet, "/api/config/snapshot-interval", nil)
	w := httptest.NewRecorder()
	HandleSnapshotInterval(w, req)

	var body map[string]int
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["interval"] != 600 {
		t.Errorf("want 600, got %d", body["interval"])
	}
}

func TestHandleSnapshotInterval_post_valid(t *testing.T) {
	stubRemount(t)
	p := writeConf(t, "")
	origConf := confFilePath
	confFilePath = p
	defer func() { confFilePath = origConf }()

	req := httptest.NewRequest(http.MethodPost, "/api/config/snapshot-interval",
		strings.NewReader(`{"interval":240}`))
	w := httptest.NewRecorder()
	HandleSnapshotInterval(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]int
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["interval"] != 240 {
		t.Errorf("want 240, got %d", body["interval"])
	}
}

func TestHandleSnapshotInterval_post_too_small(t *testing.T) {
	p := writeConf(t, "")
	origConf := confFilePath
	confFilePath = p
	defer func() { confFilePath = origConf }()

	req := httptest.NewRequest(http.MethodPost, "/api/config/snapshot-interval",
		strings.NewReader(`{"interval":10}`))
	w := httptest.NewRecorder()
	HandleSnapshotInterval(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}
