package main

// setup_ops.go — first-run setup API for the web-based wizard.
//
// API surface:
//   GET  /api/setup/status  — {configured, version, model}
//   POST /api/setup/apply   — write conf, run setup-teslausb (SSE log stream)

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ── path constants (vars so tests can redirect) ────────────────────────────────

var (
	setupConfFile        = "/root/teslausb_setup_variables.conf"
	setupFinishedFile    = "/teslausb/TESLAUSB_SETUP_FINISHED"
	setupSentinelFile    = "/boot/firmware/provisioning_done"
	setupConfigureScript = "/root/bin/setup-teslausb"
	setupDeviceModel     = "/proc/device-tree/model"
)

// ── types ──────────────────────────────────────────────────────────────────────

// SetupStatus is returned by GET /api/setup/status.
type SetupStatus struct {
	Configured bool   `json:"configured"`
	Version    string `json:"version"`
	Model      string `json:"model"`
}

// SetupApplyRequest is the body of POST /api/setup/apply.
type SetupApplyRequest struct {
	ArchiveType string `json:"archive_type"` // "cifs", "nfs", "rclone", "rsync", "none"
	ArchiveHost string `json:"archive_host"`
	ShareName   string `json:"share_name"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
}

// ── helpers ────────────────────────────────────────────────────────────────────

// isConfigured recognizes completed installs, including those without an archive server.
func isConfigured() bool {
	if _, err := os.Stat(setupFinishedFile); err == nil {
		return true
	}
	data, err := os.ReadFile(setupConfFile)
	if err != nil {
		return false
	}
	// ARCHIVE_SYSTEM covers a no-archive config from provisioning, which must
	// not send the web app back to the wizard while setup is still installing.
	re := regexp.MustCompile(`(?m)^\s*(export\s+)?(ARCHIVE_(HOST|SERVER|SYSTEM)|RSYNC_SERVER|RCLONE_DRIVE)=`)
	return re.Match(data)
}

// deviceModel reads the human-readable board model string.
func deviceModel() string {
	data, err := os.ReadFile(setupDeviceModel)
	if err != nil {
		return "unknown"
	}
	// The file contains a null-terminated C string.
	return strings.TrimRight(string(data), "\x00\n")
}

// shellQuote wraps s in POSIX single-quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// writeSetupConf updates setup keys without discarding unrelated settings.
func writeSetupConf(req SetupApplyRequest) error {
	return withConfRW(func() error {
		data, err := os.ReadFile(setupConfFile)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		content := string(data)
		if content == "" {
			content = "# Dashvault configuration — written by web setup wizard\n"
		}
		content = upsertConfKey(content, "ARCHIVE_SYSTEM", req.ArchiveType)
		switch req.ArchiveType {
		case "cifs":
			content = upsertConfKey(content, "ARCHIVE_SERVER", req.ArchiveHost)
			content = upsertConfKey(content, "SHARE_NAME", req.ShareName)
		case "nfs":
			content = upsertConfKey(content, "ARCHIVE_SERVER", req.ArchiveHost)
			content = upsertConfKey(content, "SHARE_NAME", req.ShareName)
			content = upsertConfKey(content, "NFS_SERVER", req.ArchiveHost)
			content = upsertConfKey(content, "NFS_PATH", req.ShareName)
		case "rsync":
			content = upsertConfKey(content, "RSYNC_SERVER", req.ArchiveHost)
			content = upsertConfKey(content, "RSYNC_PATH", req.ShareName)
		case "rclone":
			content = upsertConfKey(content, "RCLONE_DRIVE", req.ArchiveHost)
			content = upsertConfKey(content, "RCLONE_PATH", req.ShareName)
		}
		if req.ArchiveType == "cifs" && req.Username != "" {
			content = upsertConfKey(content, "SHARE_USER", req.Username)
		} else if req.ArchiveType == "rsync" && req.Username != "" {
			content = upsertConfKey(content, "RSYNC_USER", req.Username)
		}
		if req.ArchiveType == "cifs" && req.Password != "" {
			content = upsertConfKey(content, "SHARE_PASSWORD", req.Password)
		}
		return writeFileAtomic(setupConfFile, []byte(content), 0o600)
	})
}

// validateSetupRequest applies the archive settings rules to the wizard's
// fields, which are written to the same configuration file.
func validateSetupRequest(req SetupApplyRequest) error {
	cfg := ArchiveConfig{System: req.ArchiveType}
	switch req.ArchiveType {
	case "cifs":
		cfg.CIFS = CIFSBackendConfig{Server: req.ArchiveHost, Share: req.ShareName, User: req.Username, Password: req.Password}
	case "nfs":
		cfg.NFS = NFSBackendConfig{Server: req.ArchiveHost, Path: req.ShareName}
	case "rsync":
		cfg.Rsync = RsyncBackendConfig{User: req.Username, Server: req.ArchiveHost, Path: req.ShareName}
	case "rclone":
		cfg.Rclone = RcloneBackendConfig{Drive: req.ArchiveHost, Path: req.ShareName}
	case "none":
	default:
		return fmt.Errorf("archive_type must be cifs, nfs, rsync, rclone, or none")
	}
	if hasControlChars(req.ArchiveHost + req.ShareName + req.Username + req.Password) {
		return fmt.Errorf("archive settings must be single-line text without control characters")
	}
	return validateArchiveMountValues(cfg)
}

// ── HTTP handlers ──────────────────────────────────────────────────────────────

// HandleSetupStatus returns whether the device has been configured.
// GET /api/setup/status
func HandleSetupStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(SetupStatus{
		Configured: isConfigured(),
		Version:    currentVersion(),
		Model:      deviceModel(),
	})
}

// HandleSetupApply writes the configuration file and streams configure.sh
// output as Server-Sent Events.
//
// POST /api/setup/apply
// Body: SetupApplyRequest (JSON)
// Response: text/event-stream
//
//	data: "log line"   — progress lines from configure.sh
//	data: {"done":true} — final event on success
//	data: {"error":"…"} — final event on failure
func HandleSetupApply(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// The wizard is for first-time setup only. Applying it again would rewrite
	// the configuration, possibly while setup is still running from it.
	if isConfigured() {
		http.Error(w, `{"error":"this device is already configured"}`, http.StatusConflict)
		return
	}

	var req SetupApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	req.ArchiveType = strings.TrimSpace(req.ArchiveType)
	req.ArchiveHost = strings.TrimSpace(req.ArchiveHost)
	req.ShareName = strings.TrimSpace(req.ShareName)

	if req.ArchiveType == "" {
		req.ArchiveType = "cifs"
	}
	if req.ArchiveType != "none" {
		if req.ArchiveHost == "" || req.ShareName == "" {
			http.Error(w, `{"error":"archive_host and share_name are required"}`, http.StatusBadRequest)
			return
		}
	}

	if err := validateSetupRequest(req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// ── write conf file ─────────────────────────────────────────────────────

	if err := writeSetupConf(req); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	// ── start streaming ──────────────────────────────────────────────────────

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	sendEvent := func(line string) {
		encoded, _ := json.Marshal(line)
		fmt.Fprintf(w, "data: %s\n\n", encoded)
		flusher.Flush()
	}

	sendEvent("Configuration saved. Starting setup…")

	if _, err := os.Stat(setupConfigureScript); err != nil {
		sendJSON(w, map[string]string{"error": "setup-teslausb is not installed"})
		flusher.Flush()
		return
	}

	// configure-web.sh stops nginx, which drops this request. A request-scoped
	// command would be killed before setup can restart nginx and write the
	// completion marker, so the setup process is detached from the client.
	cmd := exec.Command(setupConfigureScript)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		sendJSON(w, map[string]string{"error": err.Error()})
		flusher.Flush()
		return
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		sendJSON(w, map[string]string{"error": err.Error()})
		flusher.Flush()
		return
	}

	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		sendEvent(sc.Text())
	}

	if err := cmd.Wait(); err != nil {
		sendJSON(w, map[string]string{"error": fmt.Sprintf("setup-teslausb: %v", err)})
		flusher.Flush()
		return
	}
	if err := writeSetupSentinel(setupSentinelFile); err != nil {
		sendJSON(w, map[string]string{"error": fmt.Sprintf("setup succeeded, but completion marker failed: %v", err)})
		flusher.Flush()
		return
	}

	sendJSON(w, map[string]bool{"done": true})
	flusher.Flush()
}

// ── small helpers ──────────────────────────────────────────────────────────────

var remountBoot = func(dir, mode string) error {
	return exec.Command("mount", "-o", "remount,"+mode, dir).Run()
}

func writeSetupSentinel(path string) error {
	return withConfRW(func() error {
		dir := filepath.Dir(path)
		if dir == "/boot/firmware" || dir == "/boot" {
			if err := remountBoot(dir, "rw"); err == nil {
				defer func() { _ = remountBoot(dir, "ro") }()
			}
		}
		return touchFile(path)
	})
}

func touchFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

func sendJSON(w http.ResponseWriter, v interface{}) {
	data, _ := json.Marshal(v)
	fmt.Fprintf(w, "data: %s\n\n", data)
}
