package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ── path constants (vars so tests can redirect) ────────────────────────────────

var (
	otaVersionFile    = "/root/teslausb_version"
	otaBackupDir      = "/root/teslausb_backup"
	otaLogFile        = "/var/log/teslausb_update.log"
	otaExtractDir     = "/tmp/teslausb-update-extracted"
	otaDownloadPath   = "/tmp/teslausb-update.tar.gz"
	otaInstallScript  = "/root/bin/ota_install.sh"
	otaRollbackScript = "/root/bin/ota_rollback.sh"

	// otaRollbackDoneHook is called (if non-nil) when the rollback goroutine
	// exits. Set by tests to synchronise teardown with t.TempDir cleanup.
	otaRollbackDoneHook func()
)

func otaSystemdArguments(operation, script string, arguments ...string) []string {
	command := []string{
		"systemd-run", "--wait", "--collect", "--unit=teslausb-ota-" + operation,
		"--property=Type=oneshot", script,
	}
	return append(command, arguments...)
}

var runOTAOperation = func(operation, script string, arguments ...string) error {
	return exec.Command("sudo", otaSystemdArguments(operation, script, arguments...)...).Run()
}

// ── types ──────────────────────────────────────────────────────────────────────

// releaseAsset is a single downloadable file attached to a Gitea/GitHub release.
type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// releaseInfo is the parsed response from a Gitea/GitHub releases/latest API.
type releaseInfo struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

// UpdateStatus is returned by GET /api/update/status.
type UpdateStatus struct {
	OTAEnabled           bool   `json:"ota_enabled"`
	ReleaseURLConfigured bool   `json:"release_url_configured"`
	CurrentVersion       string `json:"current_version"`
	LatestVersion        string `json:"latest_version,omitempty"`
	UpdateAvailable      bool   `json:"update_available"`
	RollbackAvailable    bool   `json:"rollback_available"`
	LastChecked          string `json:"last_checked,omitempty"`
}

// ── in-memory cache ────────────────────────────────────────────────────────────

type otaCacheState struct {
	mu          sync.Mutex
	latest      *releaseInfo
	lastChecked time.Time
}

var otaCache = &otaCacheState{}

// ── in-flight guard ──────────────────────────────────────────────────────────

var (
	otaInflightMu sync.Mutex
	otaInflight   bool
)

// tryStartOTA marks an OTA apply/rollback as in-flight and returns true, or
// returns false when one is already running. This prevents parallel apply and
// rollback goroutines from racing on otaDownloadPath/otaExtractDir. A caller
// that receives true must call finishOTA when the goroutine exits.
func tryStartOTA() bool {
	otaInflightMu.Lock()
	defer otaInflightMu.Unlock()
	if otaInflight {
		return false
	}
	otaInflight = true
	return true
}

func finishOTA() {
	otaInflightMu.Lock()
	otaInflight = false
	otaInflightMu.Unlock()
}

// ── helpers ────────────────────────────────────────────────────────────────────

// Explicit service environment overrides the shell-format setup file.
func otaConfigValue(key string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	data, err := os.ReadFile(confFilePath)
	if err != nil {
		return ""
	}
	return readConfKey(string(data), key)
}

func otaIsEnabled() bool {
	return otaConfigValue("OTA_ENABLED") == "true"
}

func otaGetReleaseURL() string {
	return otaConfigValue("OTA_RELEASE_URL")
}

// currentVersion returns the running binary's compiled-in version if set, or
// reads the on-disk version file written by ota_install.sh after each update.
func currentVersion() string {
	if version != "dev" {
		return version
	}
	data, err := os.ReadFile(otaVersionFile)
	if err != nil {
		return "dev"
	}
	return strings.TrimSpace(string(data))
}

// hasRollbackBackup returns true when at least one timestamped backup dir
// exists under otaBackupDir.
func hasRollbackBackup() bool {
	entries, err := os.ReadDir(otaBackupDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			return true
		}
	}
	return false
}

// appendOTALog appends a timestamped line to the given log file path.
// The log file lives on the read-only root filesystem, so the write happens
// inside withConfRW; failures are logged rather than silently swallowed.
func appendOTALog(path, msg string) {
	err := withConfRW(func() error {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = fmt.Fprintf(f, "[%s] %s\n", time.Now().UTC().Format("2006-01-02 15:04:05"), msg)
		return err
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: append OTA log %s failed: %v\n", path, err)
	}
}

// ── HTTP handlers ──────────────────────────────────────────────────────────────

// HandleUpdateStatus returns the current OTA state.
// GET /api/update/status
func HandleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	otaCache.mu.Lock()
	latest := otaCache.latest
	lastChecked := otaCache.lastChecked
	otaCache.mu.Unlock()

	status := UpdateStatus{
		OTAEnabled:           otaIsEnabled(),
		ReleaseURLConfigured: otaGetReleaseURL() != "",
		CurrentVersion:       currentVersion(),
		RollbackAvailable:    hasRollbackBackup(),
	}

	if latest != nil {
		status.LatestVersion = latest.TagName
		status.UpdateAvailable = latest.TagName != status.CurrentVersion
		status.LastChecked = lastChecked.UTC().Format(time.RFC3339)
	}

	_ = json.NewEncoder(w).Encode(status)
}

// HandleUpdateCheck queries the configured release URL and caches the result.
// POST /api/update/check
func HandleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if !otaIsEnabled() {
		http.Error(w, `{"error":"OTA updates are disabled; set OTA_ENABLED=true"}`, http.StatusForbidden)
		return
	}

	releaseURL := otaGetReleaseURL()
	if releaseURL == "" {
		http.Error(w, `{"error":"OTA_RELEASE_URL is not configured"}`, http.StatusBadRequest)
		return
	}

	release, err := fetchLatestRelease(releaseURL)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to fetch release: %s"}`, err), http.StatusBadGateway)
		return
	}

	otaCache.mu.Lock()
	otaCache.latest = release
	otaCache.lastChecked = time.Now()
	otaCache.mu.Unlock()

	HandleUpdateStatus(w, r)
}

// HandleUpdateApply downloads, verifies, and applies the latest release.
// POST /api/update/apply
func HandleUpdateApply(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if !otaIsEnabled() {
		http.Error(w, `{"error":"OTA updates are disabled; set OTA_ENABLED=true"}`, http.StatusForbidden)
		return
	}

	otaCache.mu.Lock()
	latest := otaCache.latest
	otaCache.mu.Unlock()

	if latest == nil {
		http.Error(w, `{"error":"no release cached; call /api/update/check first"}`, http.StatusConflict)
		return
	}

	var tgzURL, sha256URL string
	for _, a := range latest.Assets {
		switch a.Name {
		case "teslausb-update.tar.gz":
			tgzURL = a.BrowserDownloadURL
		case "SHA256SUMS":
			sha256URL = a.BrowserDownloadURL
		}
	}

	if tgzURL == "" {
		http.Error(w, `{"error":"release is missing teslausb-update.tar.gz asset"}`, http.StatusBadGateway)
		return
	}

	if !tryStartOTA() {
		http.Error(w, `{"error":"an update or rollback is already in progress"}`, http.StatusConflict)
		return
	}

	// Capture mutable globals into locals before the goroutine runs, so that
	// tests that redirect these paths don't create a race condition.
	ver := latest.TagName
	logPath := otaLogFile
	dlPath := otaDownloadPath
	exDir := otaExtractDir
	installSc := otaInstallScript
	go func() {
		defer finishOTA()
		if err := downloadVerifyAndInstall(ver, tgzURL, sha256URL, logPath, dlPath, exDir, installSc); err != nil {
			appendOTALog(logPath, fmt.Sprintf("ERROR: %v", err))
		}
	}()

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintln(w, `{"status":"update_started"}`)
}

// HandleUpdateRollback restores the most recent backup.
// POST /api/update/rollback
func HandleUpdateRollback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if !hasRollbackBackup() {
		http.Error(w, `{"error":"no backup available for rollback"}`, http.StatusConflict)
		return
	}

	if !tryStartOTA() {
		http.Error(w, `{"error":"an update or rollback is already in progress"}`, http.StatusConflict)
		return
	}

	logPath := otaLogFile
	rollbackSc := otaRollbackScript
	go func() {
		defer func() {
			if otaRollbackDoneHook != nil {
				otaRollbackDoneHook()
			}
		}()
		defer finishOTA()
		appendOTALog(logPath, "Starting rollback...")
		if err := runOTAOperation("rollback", rollbackSc); err != nil {
			appendOTALog(logPath, fmt.Sprintf("ERROR: rollback failed: %v", err))
		}
	}()

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintln(w, `{"status":"rollback_started"}`)
}

// HandleUpdateLog returns the last 100 lines of the OTA log file.
// GET /api/update/log
func HandleUpdateLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	data, err := os.ReadFile(otaLogFile)
	if err != nil {
		// No log yet is fine — return empty body.
		return
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 100 {
		lines = lines[len(lines)-100:]
	}
	fmt.Fprint(w, strings.Join(lines, "\n"))
}

// ── update pipeline ────────────────────────────────────────────────────────────

func downloadVerifyAndInstall(ver, tgzURL, sha256URL, logPath, dlPath, exDir, installSc string) error {
	appendOTALog(logPath, fmt.Sprintf("=== Starting update to %s ===", ver))

	appendOTALog(logPath, fmt.Sprintf("Downloading %s", tgzURL))
	if err := downloadFile(tgzURL, dlPath); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	if sha256URL != "" {
		appendOTALog(logPath, "Verifying SHA256...")
		if err := verifySHA256(dlPath, sha256URL); err != nil {
			return fmt.Errorf("SHA256: %w", err)
		}
		appendOTALog(logPath, "SHA256 OK")
	} else {
		appendOTALog(logPath, "WARNING: no SHA256SUMS asset in release — skipping checksum verification")
	}

	appendOTALog(logPath, "Extracting tarball...")
	if err := os.RemoveAll(exDir); err != nil {
		return fmt.Errorf("clean extract dir: %w", err)
	}
	if err := os.MkdirAll(exDir, 0o755); err != nil {
		return fmt.Errorf("mkdir extract dir: %w", err)
	}
	if err := extractTar(dlPath, exDir); err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	appendOTALog(logPath, "Handing off to independent systemd installer (service will restart)...")
	return runOTAOperation("apply", installSc, exDir)
}

func downloadFile(url, dest string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "teslausb-ota/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

func verifySHA256(tgzPath, sha256URL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sha256URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "teslausb-ota/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	sumsData, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// Parse "sha256  filename" lines — accept both BSD and GNU format.
	var expectedHash string
	for _, line := range strings.Split(string(sumsData), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.HasSuffix(fields[len(fields)-1], "teslausb-update.tar.gz") {
			expectedHash = fields[0]
			break
		}
	}
	if expectedHash == "" {
		return fmt.Errorf("teslausb-update.tar.gz not found in SHA256SUMS")
	}

	f, err := os.Open(tgzPath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}

	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expectedHash {
		return fmt.Errorf("SHA256 mismatch: got %s, expected %s", actual, expectedHash)
	}
	return nil
}

// extractTar extracts a .tar.gz file into dest, refusing path-traversal entries.
func extractTar(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	destClean := filepath.Clean(dest) + string(os.PathSeparator)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Security: reject entries whose cleaned name starts with ".." or is
		// absolute, or that resolve to a path outside dest after joining.
		cleanedName := filepath.Clean(hdr.Name)
		if cleanedName == ".." || strings.HasPrefix(cleanedName, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("path traversal attempt in tar: %s", hdr.Name)
		}
		target := filepath.Join(dest, cleanedName)
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), destClean) {
			return fmt.Errorf("path traversal attempt in tar: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)|0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		default:
			// Reject symlinks, hardlinks, devices, fifos. A symlink target is
			// not constrained by destClean, so a malicious tarball could plant
			// e.g. bin/teslausb-backend -> /etc/shadow and a later regular file
			// in the same archive would write through it.
			return fmt.Errorf("disallowed tar entry type %c for %s", hdr.Typeflag, hdr.Name)
		}
	}
	return nil
}

func fetchLatestRelease(url string) (*releaseInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "teslausb-ota/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release API returned HTTP %d", resp.StatusCode)
	}

	var release releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	if release.TagName == "" {
		return nil, fmt.Errorf("release response missing tag_name")
	}
	return &release, nil
}
