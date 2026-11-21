package main

// archive_config.go — read/write archive backend configuration in
// /root/teslausb_setup_variables.conf.
//
// All backends' credentials coexist in the conf file at all times.
// Switching the active backend (ARCHIVE_SYSTEM) never discards other
// backends' settings.
//
// API surface:
//   GET  /api/config/archive  — returns active backend + all backends' configs
//   POST /api/config/archive  — writes all backends, sets active, restarts archiveloop

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ── types ─────────────────────────────────────────────────────────────────────

type CIFSBackendConfig struct {
	Server string `json:"server"`
	Share  string `json:"share"`
	User   string `json:"user"`
	// Password is write-only: a GET never returns it. An empty password in a
	// POST keeps the saved one; set ClearPassword to remove it.
	Password string `json:"password"`
	// PasswordSet (GET only) says whether a password is saved.
	PasswordSet bool `json:"password_set"`
	// ClearPassword (POST only) removes the saved password.
	ClearPassword bool   `json:"clear_password,omitempty"`
	Domain        string `json:"domain,omitempty"`
	Version       string `json:"version,omitempty"`
	Sec           string `json:"sec,omitempty"`
}

type NFSBackendConfig struct {
	Server string `json:"server"`
	Path   string `json:"path"`
}

type RsyncBackendConfig struct {
	User   string `json:"user"`
	Server string `json:"server"`
	Path   string `json:"path"`
}

type RcloneBackendConfig struct {
	Drive string `json:"drive"`
	Path  string `json:"path"`
}

// ArchiveConfig carries every backend's credentials simultaneously.
// The active backend is indicated by System.
type ArchiveConfig struct {
	System      string `json:"system"` // "cifs" | "nfs" | "rsync" | "rclone" | "none"
	SavedClips  bool   `json:"saved_clips"`
	SentryClips bool   `json:"sentry_clips"`
	RecentClips bool   `json:"recent_clips"`
	// RecentCameras lists the cameras whose RecentClips are archived. Omitted
	// (null) in a POST leaves the setting unchanged; GET always returns it.
	RecentCameras []string `json:"recent_cameras"`
	// RecentPruneSnapshots (experimental) also deletes the other cameras'
	// RecentClips from the Pi's snapshots. Omitted in a POST keeps the setting.
	RecentPruneSnapshots *bool `json:"recent_prune_snapshots"`
	// DetectedCameras (GET only) lists the cameras found in clip file names on
	// the Pi, so the UI offers only cameras this car records. Empty when no
	// clips are present yet.
	DetectedCameras []string `json:"detected_cameras"`

	CIFS   CIFSBackendConfig   `json:"cifs"`
	NFS    NFSBackendConfig    `json:"nfs"`
	Rsync  RsyncBackendConfig  `json:"rsync"`
	Rclone RcloneBackendConfig `json:"rclone"`
}

// RecentCameraNames are the cameras the car records, in display order.
// HW4 cars add the pillar cameras.
var RecentCameraNames = []string{"front", "back", "left_repeater", "right_repeater", "left_pillar", "right_pillar"}

// detectRecentCameras returns the cameras that appear in clip file names under
// root (RecentClips/<date>/ and SavedClips|SentryClips/<event>/), in display
// order. Only directory entries are read, never the videos, and the newest
// folders of each kind are checked first.
func detectRecentCameras(root string) []string {
	found := map[string]bool{}
	scan := func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			name := entry.Name()
			for _, camera := range RecentCameraNames {
				if strings.HasSuffix(name, "-"+camera+".mp4") {
					found[camera] = true
				}
			}
		}
	}
	for _, category := range []string{"RecentClips", "SavedClips", "SentryClips"} {
		folders, err := os.ReadDir(filepath.Join(root, category))
		if err != nil {
			continue
		}
		// Newest first; a few folders are enough to see every camera.
		for i, checked := len(folders)-1, 0; i >= 0 && checked < 5; i-- {
			if folders[i].IsDir() {
				scan(filepath.Join(root, category, folders[i].Name()))
				checked++
			}
		}
		scan(filepath.Join(root, category)) // flat RecentClips layout
		if len(found) == len(RecentCameraNames) {
			break
		}
	}
	cameras := []string{}
	for _, camera := range RecentCameraNames {
		if found[camera] {
			cameras = append(cameras, camera)
		}
	}
	return cameras
}

// parseRecentCameras reads RECENTCLIPS_CAMERAS. Empty means all cameras.
func parseRecentCameras(value string) []string {
	wanted := map[string]bool{}
	for _, name := range strings.FieldsFunc(value, func(r rune) bool { return r == ' ' || r == ',' }) {
		wanted[name] = true
	}
	cameras := []string{}
	for _, name := range RecentCameraNames {
		if len(wanted) == 0 || wanted[name] {
			cameras = append(cameras, name)
		}
	}
	return cameras
}

// recentCamerasConfValue validates a camera selection and returns the conf
// value: "" when every camera is selected, else the names in display order.
func recentCamerasConfValue(cameras []string) (string, error) {
	known := map[string]bool{}
	for _, name := range RecentCameraNames {
		known[name] = true
	}
	selected := map[string]bool{}
	for _, name := range cameras {
		if !known[name] {
			return "", fmt.Errorf("unknown camera %q", name)
		}
		selected[name] = true
	}
	if len(selected) == 0 {
		return "", fmt.Errorf("select at least one RecentClips camera, or turn RecentClips archiving off")
	}
	if len(selected) == len(RecentCameraNames) {
		return "", nil
	}
	ordered := []string{}
	for _, name := range RecentCameraNames {
		if selected[name] {
			ordered = append(ordered, name)
		}
	}
	return strings.Join(ordered, " "), nil
}

// ── conf helpers ──────────────────────────────────────────────────────────────

// readConfKey reads the value of `export KEY=...` from the conf file.
// Returns "" if the key is missing or commented out.
func readConfKey(content, key string) string {
	re := regexp.MustCompile(`(?m)^export ` + regexp.QuoteMeta(key) + `=(.*)$`)
	m := re.FindStringSubmatch(content)
	if m == nil {
		return ""
	}
	v := strings.TrimSpace(m[1])
	// Strip surrounding single or double quotes
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		v = strings.ReplaceAll(v[1:len(v)-1], `'\''`, "'")
	} else if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	return v
}

func readConfBool(content, key string, fallback bool) bool {
	value := readConfKey(content, key)
	if value == "" {
		return fallback
	}
	return strings.EqualFold(value, "true")
}

// upsertConfKey sets `export KEY='value'` in content, replacing any existing
// (active or commented-out) line for that key. If the key is absent it is
// appended before the trailing newline.
//
// The file is sourced by root's shell and parsed line by line elsewhere, so a
// value must stay on one line: control characters are dropped here even though
// the request handlers already reject them.
func upsertConfKey(content, key, value string) string {
	line := fmt.Sprintf("export %s=%s", key, shellQuote(stripControlChars(value)))
	re := regexp.MustCompile(`(?m)^#?\s*export ` + regexp.QuoteMeta(key) + `=.*$`)
	if re.MatchString(content) {
		return re.ReplaceAllStringFunc(content, func(string) string { return line })
	}
	return strings.TrimRight(content, "\n") + "\n" + line + "\n"
}

// commentOutKey comments out any active `export KEY=` line.
// Kept for use by other callers (e.g. snapshot interval).
func commentOutKey(content, key string) string {
	re := regexp.MustCompile(`(?m)^export ` + regexp.QuoteMeta(key) + `=(.*)$`)
	return re.ReplaceAllStringFunc(content, func(s string) string {
		return "#" + s
	})
}

// remountRoot remounts / with the given mode ("rw" or "ro").
// A var so tests can stub out the mount syscall.
var remountRoot = func(mode string) error {
	return exec.Command("mount", "-o", "remount,"+mode, "/").Run()
}

var cifsCredentialsFilePath = "/root/.teslaCamArchiveCredentials"

// withConfRW remounts / read-write, calls fn, then remounts read-only again.
// Any remount error is returned immediately without calling fn.
func withConfRW(fn func() error) error {
	return withRootFSLock(func() error {
		if err := remountRoot("rw"); err != nil {
			return fmt.Errorf("remount rw: %w", err)
		}
		fnErr := fn()
		if err := remountRoot("ro"); err != nil {
			fmt.Fprintf(os.Stderr, "warning: remount ro failed: %v\n", err)
		}
		return fnErr
	})
}

// ── read ──────────────────────────────────────────────────────────────────────

// readArchiveConfig reads ALL backends' credentials from the conf file
// simultaneously, regardless of which backend is active.
func readArchiveConfig() (ArchiveConfig, error) {
	data, err := os.ReadFile(confFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			prune := false
			return ArchiveConfig{System: "none", SavedClips: true, SentryClips: true, RecentCameras: parseRecentCameras(""), RecentPruneSnapshots: &prune}, nil
		}
		return ArchiveConfig{}, err
	}
	c := string(data)

	system := strings.ToLower(readConfKey(c, "ARCHIVE_SYSTEM"))
	if system == "" {
		system = "none"
	}

	nfsServer := readConfKey(c, "NFS_SERVER")
	nfsPath := readConfKey(c, "NFS_PATH")
	if system == "nfs" {
		if nfsServer == "" {
			nfsServer = readConfKey(c, "ARCHIVE_SERVER")
		}
		if nfsPath == "" {
			nfsPath = readConfKey(c, "SHARE_NAME")
		}
	}

	prune := readConfBool(c, "RECENTCLIPS_PRUNE_SNAPSHOTS", false)
	return ArchiveConfig{
		RecentPruneSnapshots: &prune,
		System:               system,
		SavedClips:           readConfBool(c, "ARCHIVE_SAVEDCLIPS", true),
		SentryClips:          readConfBool(c, "ARCHIVE_SENTRYCLIPS", true),
		RecentClips:          readConfBool(c, "ARCHIVE_RECENTCLIPS", false),
		RecentCameras:        parseRecentCameras(readConfKey(c, "RECENTCLIPS_CAMERAS")),
		CIFS: CIFSBackendConfig{
			Server:   readConfKey(c, "ARCHIVE_SERVER"),
			Share:    readConfKey(c, "SHARE_NAME"),
			User:     readConfKey(c, "SHARE_USER"),
			Password: readConfKey(c, "SHARE_PASSWORD"),
			Domain:   readConfKey(c, "SHARE_DOMAIN"),
			Version:  readConfKey(c, "CIFS_VERSION"),
			Sec:      readConfKey(c, "CIFS_SEC"),
		},
		NFS: NFSBackendConfig{
			Server: nfsServer,
			Path:   nfsPath,
		},
		Rsync: RsyncBackendConfig{
			User:   readConfKey(c, "RSYNC_USER"),
			Server: readConfKey(c, "RSYNC_SERVER"),
			Path:   readConfKey(c, "RSYNC_PATH"),
		},
		Rclone: RcloneBackendConfig{
			Drive: readConfKey(c, "RCLONE_DRIVE"),
			Path:  readConfKey(c, "RCLONE_PATH"),
		},
	}, nil
}

// ── write ─────────────────────────────────────────────────────────────────────

// writeArchiveConfig writes all backends' credentials to the conf file.
// Keys from non-active backends are written (or updated) too — credentials
// are never lost when switching the active backend.
func writeArchiveConfig(cfg ArchiveConfig) error {
	return withConfRW(func() error {
		data, err := os.ReadFile(confFilePath)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		c := string(data)

		c = upsertConfKey(c, "ARCHIVE_SYSTEM", cfg.System)
		c = upsertConfKey(c, "ARCHIVE_SAVEDCLIPS", strconv.FormatBool(cfg.SavedClips))
		c = upsertConfKey(c, "ARCHIVE_SENTRYCLIPS", strconv.FormatBool(cfg.SentryClips))
		c = upsertConfKey(c, "ARCHIVE_RECENTCLIPS", strconv.FormatBool(cfg.RecentClips))
		if cfg.RecentCameras != nil {
			cameras, err := recentCamerasConfValue(cfg.RecentCameras)
			if err != nil {
				return err
			}
			c = upsertConfKey(c, "RECENTCLIPS_CAMERAS", cameras)
		}
		if cfg.RecentPruneSnapshots != nil {
			c = upsertConfKey(c, "RECENTCLIPS_PRUNE_SNAPSHOTS", strconv.FormatBool(*cfg.RecentPruneSnapshots))
		}

		// CIFS / Samba
		archiveServer := cfg.CIFS.Server
		shareName := cfg.CIFS.Share
		if cfg.System == "nfs" {
			if archiveServer == "" {
				archiveServer = cfg.NFS.Server
			}
			if shareName == "" {
				shareName = cfg.NFS.Path
			}
		}
		c = upsertConfKey(c, "ARCHIVE_SERVER", archiveServer)
		c = upsertConfKey(c, "SHARE_NAME", shareName)
		c = upsertConfKey(c, "SHARE_USER", cfg.CIFS.User)
		c = upsertConfKey(c, "SHARE_PASSWORD", cfg.CIFS.Password)
		c = upsertConfKey(c, "SHARE_DOMAIN", cfg.CIFS.Domain)
		c = upsertConfKey(c, "CIFS_VERSION", cfg.CIFS.Version)
		c = upsertConfKey(c, "CIFS_SEC", cfg.CIFS.Sec)

		// NFS
		c = upsertConfKey(c, "NFS_SERVER", cfg.NFS.Server)
		c = upsertConfKey(c, "NFS_PATH", cfg.NFS.Path)

		// rsync
		c = upsertConfKey(c, "RSYNC_USER", cfg.Rsync.User)
		c = upsertConfKey(c, "RSYNC_SERVER", cfg.Rsync.Server)
		c = upsertConfKey(c, "RSYNC_PATH", cfg.Rsync.Path)

		// rclone
		c = upsertConfKey(c, "RCLONE_DRIVE", cfg.Rclone.Drive)
		c = upsertConfKey(c, "RCLONE_PATH", cfg.Rclone.Path)

		return applyArchiveConfig(cfg, c)
	})
}

// ── HTTP handlers ─────────────────────────────────────────────────────────────

// redactArchiveConfig prepares a config for an API response: the share
// password stays on the Pi and the client only learns whether one is saved.
func redactArchiveConfig(cfg ArchiveConfig) ArchiveConfig {
	cfg.CIFS.PasswordSet = cfg.CIFS.Password != ""
	cfg.CIFS.Password = ""
	cfg.CIFS.ClearPassword = false
	return cfg
}

// HandleArchiveConfig reads (GET) or writes (POST) the archive backend config.
//
// GET  /api/config/archive
// POST /api/config/archive  body: ArchiveConfig JSON
func HandleArchiveConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodGet {
		cfg, err := readArchiveConfig()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		cfg.DetectedCameras = detectRecentCameras(teslaCamRoot)
		_ = json.NewEncoder(w).Encode(redactArchiveConfig(cfg))
		return
	}

	// POST — decode full config
	var cfg ArchiveConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if err := validateArchiveMountValues(cfg); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if cfg.RecentCameras != nil {
		if _, err := recentCamerasConfValue(cfg.RecentCameras); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// The client never receives the saved share password, so an empty one
	// means "unchanged" unless it asks for the password to be removed.
	if cfg.CIFS.Password == "" && !cfg.CIFS.ClearPassword {
		saved, err := readArchiveConfig()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		cfg.CIFS.Password = saved.CIFS.Password
	}

	// Validate only the active backend's required fields.
	switch cfg.System {
	case "cifs":
		if cfg.CIFS.Server == "" || cfg.CIFS.Share == "" {
			writeJSONError(w, http.StatusBadRequest, "cifs requires server and share")
			return
		}
	case "nfs":
		if cfg.NFS.Server == "" || cfg.NFS.Path == "" {
			writeJSONError(w, http.StatusBadRequest, "nfs requires server and path")
			return
		}
	case "rsync":
		if cfg.Rsync.Server == "" || cfg.Rsync.Path == "" {
			writeJSONError(w, http.StatusBadRequest, "rsync requires server and path")
			return
		}
	case "rclone":
		if cfg.Rclone.Drive == "" {
			writeJSONError(w, http.StatusBadRequest, "rclone requires drive")
			return
		}
	case "none", "":
		cfg.System = "none"
	default:
		writeJSONError(w, http.StatusBadRequest, "system must be cifs, nfs, rsync, rclone, or none")
		return
	}

	if err := writeArchiveConfig(cfg); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Return the freshly-read config as confirmation.
	cfg, err := readArchiveConfig()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg.DetectedCameras = detectRecentCameras(teslaCamRoot)
	_ = json.NewEncoder(w).Encode(redactArchiveConfig(cfg))
}
