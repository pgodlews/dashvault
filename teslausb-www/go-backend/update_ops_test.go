package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── helpers ────────────────────────────────────────────────────────────────────

func redirectOTAPaths(t *testing.T) (backupDir, logFile string) {
	t.Helper()
	tmp := t.TempDir()
	backupDir = filepath.Join(tmp, "backup")
	logFile = filepath.Join(tmp, "update.log")

	orig := struct {
		vf, bd, lf, ed, dp, is, rs string
	}{otaVersionFile, otaBackupDir, otaLogFile, otaExtractDir, otaDownloadPath, otaInstallScript, otaRollbackScript}

	otaVersionFile = filepath.Join(tmp, "teslausb_version")
	otaBackupDir = backupDir
	otaLogFile = logFile
	otaExtractDir = filepath.Join(tmp, "extract")
	otaDownloadPath = filepath.Join(tmp, "update.tar.gz")
	otaInstallScript = "/bin/true" // no-op
	otaRollbackScript = "/bin/true"
	originalRunOTAOperation := runOTAOperation
	runOTAOperation = func(string, string, ...string) error { return nil }

	t.Cleanup(func() {
		runOTAOperation = originalRunOTAOperation
		otaVersionFile = orig.vf
		otaBackupDir = orig.bd
		otaLogFile = orig.lf
		otaExtractDir = orig.ed
		otaDownloadPath = orig.dp
		otaInstallScript = orig.is
		otaRollbackScript = orig.rs
	})
	return
}

func setOTAEnv(t *testing.T, enabled bool, url string) {
	t.Helper()
	if enabled {
		t.Setenv("OTA_ENABLED", "true")
	} else {
		t.Setenv("OTA_ENABLED", "false")
	}
	t.Setenv("OTA_RELEASE_URL", url)
}

// buildTarGz creates an in-memory .tar.gz with a single file at the given path
// and content, returning the bytes and its SHA256 hex string.
func buildTarGz(t *testing.T, name, content string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	body := []byte(content)
	hdr := &tar.Header{
		Name:     name,
		Mode:     0o644,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gw.Close()

	raw := buf.Bytes()
	h := sha256.Sum256(raw)
	return raw, hex.EncodeToString(h[:])
}

// mockReleaseServer returns a test server that serves a fake release API,
// the tarball, and a SHA256SUMS file.
func mockReleaseServer(t *testing.T, tag string, tgzBytes []byte, sha256hex string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"tag_name": tag,
				"assets": []map[string]string{
					{
						"name":                 "teslausb-update.tar.gz",
						"browser_download_url": fmt.Sprintf("http://%s/dl/teslausb-update.tar.gz", r.Host),
					},
					{
						"name":                 "SHA256SUMS",
						"browser_download_url": fmt.Sprintf("http://%s/dl/SHA256SUMS", r.Host),
					},
				},
			})
		case r.URL.Path == "/dl/teslausb-update.tar.gz":
			w.Write(tgzBytes)
		case r.URL.Path == "/dl/SHA256SUMS":
			fmt.Fprintf(w, "%s  teslausb-update.tar.gz\n", sha256hex)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ── unit tests ─────────────────────────────────────────────────────────────────

func TestCurrentVersion_compiledIn(t *testing.T) {
	orig := version
	version = "v9.9.9"
	t.Cleanup(func() { version = orig })

	if got := currentVersion(); got != "v9.9.9" {
		t.Errorf("want v9.9.9, got %s", got)
	}
}

func TestCurrentVersion_fromFile(t *testing.T) {
	redirectOTAPaths(t)
	orig := version
	version = "dev"
	t.Cleanup(func() { version = orig })

	if err := os.WriteFile(otaVersionFile, []byte("v1.2.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := currentVersion(); got != "v1.2.3" {
		t.Errorf("want v1.2.3, got %s", got)
	}
}

func TestCurrentVersion_missingFile(t *testing.T) {
	redirectOTAPaths(t)
	orig := version
	version = "dev"
	t.Cleanup(func() { version = orig })

	if got := currentVersion(); got != "dev" {
		t.Errorf("want dev, got %s", got)
	}
}

func TestHasRollbackBackup_absent(t *testing.T) {
	redirectOTAPaths(t)
	if hasRollbackBackup() {
		t.Error("expected no backup")
	}
}

func TestHasRollbackBackup_present(t *testing.T) {
	redirectOTAPaths(t)
	if err := os.MkdirAll(filepath.Join(otaBackupDir, "2024-01-01T120000"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !hasRollbackBackup() {
		t.Error("expected backup found")
	}
}

func TestHandleUpdateStatus_disabled(t *testing.T) {
	redirectOTAPaths(t)
	setOTAEnv(t, false, "")

	// Reset cache
	otaCache.mu.Lock()
	otaCache.latest = nil
	otaCache.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/update/status", nil)
	w := httptest.NewRecorder()
	HandleUpdateStatus(w, req)

	var s UpdateStatus
	if err := json.NewDecoder(w.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s.OTAEnabled {
		t.Error("expected OTAEnabled=false")
	}
	if s.ReleaseURLConfigured {
		t.Error("expected ReleaseURLConfigured=false")
	}
}

func TestHandleUpdateCheck_disabledReturns403(t *testing.T) {
	redirectOTAPaths(t)
	setOTAEnv(t, false, "http://example.com")

	req := httptest.NewRequest(http.MethodPost, "/api/update/check", nil)
	w := httptest.NewRecorder()
	HandleUpdateCheck(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestHandleUpdateCheck_noURLReturns400(t *testing.T) {
	redirectOTAPaths(t)
	setOTAEnv(t, true, "")

	req := httptest.NewRequest(http.MethodPost, "/api/update/check", nil)
	w := httptest.NewRecorder()
	HandleUpdateCheck(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestHandleUpdateCheck_success(t *testing.T) {
	redirectOTAPaths(t)
	tgz, _ := buildTarGz(t, "VERSION", "v2.0.0")
	srv := mockReleaseServer(t, "v2.0.0", tgz, "abc123")
	setOTAEnv(t, true, srv.URL+"/releases/latest")

	// Reset cache
	otaCache.mu.Lock()
	otaCache.latest = nil
	otaCache.mu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/api/update/check", nil)
	w := httptest.NewRecorder()
	HandleUpdateCheck(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var s UpdateStatus
	if err := json.NewDecoder(w.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s.LatestVersion != "v2.0.0" {
		t.Errorf("want LatestVersion=v2.0.0, got %s", s.LatestVersion)
	}
	if s.LastChecked == "" {
		t.Error("expected LastChecked to be set")
	}
}

func TestHandleUpdateApply_disabledReturns403(t *testing.T) {
	redirectOTAPaths(t)
	setOTAEnv(t, false, "")

	req := httptest.NewRequest(http.MethodPost, "/api/update/apply", nil)
	w := httptest.NewRecorder()
	HandleUpdateApply(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", w.Code)
	}
}

func TestHandleUpdateApply_noCacheReturns409(t *testing.T) {
	redirectOTAPaths(t)
	setOTAEnv(t, true, "http://example.com")

	otaCache.mu.Lock()
	otaCache.latest = nil
	otaCache.mu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/api/update/apply", nil)
	w := httptest.NewRecorder()
	HandleUpdateApply(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("want 409, got %d", w.Code)
	}
}

func TestHandleUpdateRollback_noBackupReturns409(t *testing.T) {
	redirectOTAPaths(t)

	req := httptest.NewRequest(http.MethodPost, "/api/update/rollback", nil)
	w := httptest.NewRecorder()
	HandleUpdateRollback(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("want 409, got %d", w.Code)
	}
}

func TestHandleUpdateRollback_accepted(t *testing.T) {
	redirectOTAPaths(t)
	stubRemount(t) // the rollback goroutine appends to the OTA log via withConfRW
	// Create a fake backup dir so hasRollbackBackup() returns true.
	if err := os.MkdirAll(filepath.Join(otaBackupDir, "2024-01-01T120000"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Wire up the done-hook so we can wait for the goroutine to finish
	// before t.TempDir's cleanup removes the directory it writes into.
	done := make(chan struct{})
	otaRollbackDoneHook = func() { close(done) }
	t.Cleanup(func() {
		<-done // block until goroutine has exited
		otaRollbackDoneHook = nil
	})

	req := httptest.NewRequest(http.MethodPost, "/api/update/rollback", nil)
	w := httptest.NewRecorder()
	HandleUpdateRollback(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("want 202, got %d", w.Code)
	}
}

func TestHandleUpdateLog_empty(t *testing.T) {
	redirectOTAPaths(t)
	req := httptest.NewRequest(http.MethodGet, "/api/update/log", nil)
	w := httptest.NewRecorder()
	HandleUpdateLog(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
}

func TestHandleUpdateLog_content(t *testing.T) {
	redirectOTAPaths(t)
	if err := os.WriteFile(otaLogFile, []byte("[2024] line one\n[2024] line two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/update/log", nil)
	w := httptest.NewRecorder()
	HandleUpdateLog(w, req)
	if !strings.Contains(w.Body.String(), "line one") {
		t.Errorf("expected log content, got: %s", w.Body)
	}
}

func TestVerifySHA256_match(t *testing.T) {
	redirectOTAPaths(t)
	tgz, hash := buildTarGz(t, "test.txt", "hello")
	if err := os.WriteFile(otaDownloadPath, tgz, 0o644); err != nil {
		t.Fatal(err)
	}

	sums := []byte(fmt.Sprintf("%s  teslausb-update.tar.gz\n", hash))
	if err := verifySHA256(otaDownloadPath, sums); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestVerifySHA256_mismatch(t *testing.T) {
	redirectOTAPaths(t)
	tgz, _ := buildTarGz(t, "test.txt", "hello")
	if err := os.WriteFile(otaDownloadPath, tgz, 0o644); err != nil {
		t.Fatal(err)
	}

	sums := []byte(fmt.Sprintf("%s  teslausb-update.tar.gz\n", strings.Repeat("0", 64)))
	if err := verifySHA256(otaDownloadPath, sums); err == nil {
		t.Error("expected mismatch error")
	}
}

func TestVerifySHA256_ignoresSimilarFileName(t *testing.T) {
	redirectOTAPaths(t)
	tgz, hash := buildTarGz(t, "test.txt", "hello")
	if err := os.WriteFile(otaDownloadPath, tgz, 0o644); err != nil {
		t.Fatal(err)
	}

	sums := []byte(fmt.Sprintf("%s  other-teslausb-update.tar.gz\n", hash))
	if err := verifySHA256(otaDownloadPath, sums); err == nil {
		t.Error("expected a checksum for another file to be ignored")
	}
}

func TestExtractTar_pathTraversal(t *testing.T) {
	redirectOTAPaths(t)
	dest := t.TempDir()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	// Attempt path traversal
	tw.WriteHeader(&tar.Header{Name: "../evil.txt", Mode: 0o644, Size: 4, Typeflag: tar.TypeReg})
	tw.Write([]byte("evil"))
	tw.Close()
	gw.Close()

	if err := extractTar(func() string {
		f, _ := os.CreateTemp(dest, "test*.tar.gz")
		f.Write(buf.Bytes())
		f.Close()
		return f.Name()
	}(), t.TempDir()); err == nil {
		t.Error("expected path traversal error")
	}
}

// TestExtractTar_rejectsSymlink ensures symlinks in update tarballs are
// refused outright. A symlink target is not constrained by destClean; without
// this check, a malicious tarball could plant a dangling symlink and a later
// regular-file entry would write through it to anywhere on the filesystem.
func TestExtractTar_rejectsSymlink(t *testing.T) {
	redirectOTAPaths(t)
	dest := t.TempDir()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	tw.WriteHeader(&tar.Header{
		Name:     "evil-link",
		Linkname: "/etc/shadow",
		Typeflag: tar.TypeSymlink,
		Mode:     0o777,
	})
	tw.Close()
	gw.Close()

	tgz := filepath.Join(dest, "in.tar.gz")
	if err := os.WriteFile(tgz, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := extractTar(tgz, t.TempDir()); err == nil {
		t.Error("expected symlink rejection error")
	}
}

func TestFetchLatestRelease_success(t *testing.T) {
	tgz, hash := buildTarGz(t, "VERSION", "v3.0.0")
	srv := mockReleaseServer(t, "v3.0.0", tgz, hash)

	rel, err := fetchLatestRelease(srv.URL + "/releases/latest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rel.TagName != "v3.0.0" {
		t.Errorf("want v3.0.0, got %s", rel.TagName)
	}
	if len(rel.Assets) != 2 {
		t.Errorf("want 2 assets, got %d", len(rel.Assets))
	}
}

func TestHandleUpdateApply_inFlightReturns409(t *testing.T) {
	redirectOTAPaths(t)
	setOTAEnv(t, true, "http://example.com")

	otaCache.mu.Lock()
	otaCache.latest = &releaseInfo{
		TagName: "v9.9.9",
		Assets: []releaseAsset{
			{Name: "teslausb-update.tar.gz", BrowserDownloadURL: "http://example.com/x"},
			{Name: "SHA256SUMS", BrowserDownloadURL: "http://example.com/sums"},
			{Name: "SHA256SUMS.minisig", BrowserDownloadURL: "http://example.com/sig"},
		},
	}
	otaCache.mu.Unlock()
	t.Cleanup(func() {
		otaCache.mu.Lock()
		otaCache.latest = nil
		otaCache.mu.Unlock()
	})

	// Simulate an update/rollback already running.
	if !tryStartOTA() {
		t.Fatal("expected to acquire the OTA in-flight slot")
	}
	t.Cleanup(finishOTA)

	req := httptest.NewRequest(http.MethodPost, "/api/update/apply", nil)
	w := httptest.NewRecorder()
	HandleUpdateApply(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("want 409, got %d", w.Code)
	}
}

func TestHandleUpdateRollback_inFlightReturns409(t *testing.T) {
	redirectOTAPaths(t)
	if err := os.MkdirAll(filepath.Join(otaBackupDir, "2024-01-01T120000"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Simulate an update/rollback already running.
	if !tryStartOTA() {
		t.Fatal("expected to acquire the OTA in-flight slot")
	}
	t.Cleanup(finishOTA)

	req := httptest.NewRequest(http.MethodPost, "/api/update/rollback", nil)
	w := httptest.NewRecorder()
	HandleUpdateRollback(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("want 409, got %d", w.Code)
	}
}

// Note: the flag-reset path is covered transitively — TestHandleUpdateRollback_accepted
// runs its goroutine to completion before these tests, and they could not acquire
// the in-flight slot above if finishOTA had not run.

func TestOTAOperationsUseIndependentTransientServices(t *testing.T) {
	for _, operation := range []string{"apply", "rollback"} {
		arguments := otaSystemdArguments(operation, "/root/bin/ota_"+operation+".sh", "/tmp/release")
		joined := strings.Join(arguments, " ")
		for _, expected := range []string{"systemd-run", "--wait", "--collect", "teslausb-ota-" + operation, "Type=oneshot"} {
			if !strings.Contains(joined, expected) {
				t.Errorf("%s arguments %q missing %q", operation, joined, expected)
			}
		}
	}
}

func TestOTAReadsInstalledSetupConfiguration(t *testing.T) {
	original := confFilePath
	confFilePath = writeConf(t, "export OTA_ENABLED='true'\nexport OTA_RELEASE_URL='https://releases.example/api/latest'\n")
	t.Cleanup(func() { confFilePath = original })
	// A systemd-launched backend does not inherit envsetup.sh's exports.
	for _, key := range []string{"OTA_ENABLED", "OTA_RELEASE_URL"} {
		value, exists := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if exists {
				_ = os.Setenv(key, value)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
	if !otaIsEnabled() || otaGetReleaseURL() != "https://releases.example/api/latest" {
		t.Fatal("setup configuration not loaded")
	}
	w := httptest.NewRecorder()
	HandleUpdateStatus(w, httptest.NewRequest("GET", "/api/update/status", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ota_enabled":true`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	t.Setenv("OTA_ENABLED", "false")
	t.Setenv("OTA_RELEASE_URL", "")
	if otaIsEnabled() || otaGetReleaseURL() != "" {
		t.Fatal("explicit service environment must override setup")
	}
}
