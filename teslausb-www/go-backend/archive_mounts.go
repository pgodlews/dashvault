package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var archiveFstabPath = "/etc/fstab"
var archiveReconfigure = func(args ...string) error {
	// Mount/service orchestration stays in the shell layer.
	cmd := exec.Command("/root/bin/reconfigure-archive.sh", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// stderr carries the reason, e.g. which dependency could not be installed.
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return fmt.Errorf("archive %s failed (archiving may be stopped): %w: %s", args[0], err, lastLines(detail, 6))
		}
		return fmt.Errorf("archive %s failed (archiving may be stopped): %w", args[0], err)
	}
	return nil
}

// lastLines keeps an error message short when a script prints a lot.
func lastLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "; ")
}

// hasControlChars reports whether s holds anything that could break the
// line-based configuration file or the tools that read it: ASCII and C1
// control characters (newline, tab, NUL, escape ...), the Unicode line and
// paragraph separators, or invalid UTF-8.
func hasControlChars(s string) bool {
	if !utf8.ValidString(s) {
		return true
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return true
		}
	}
	return false
}

// stripControlChars removes what hasControlChars rejects.
func stripControlChars(s string) string {
	if !hasControlChars(s) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r == utf8.RuneError || unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, ""))
}

// rsync runs "ssh user@server" and "rsync ... user@server:path"; rclone gets
// "drive:path". A value starting with "-" would be read as a command-line
// option (ssh -oProxyCommand=... runs a command), so user, server and remote
// names are limited to the characters those names can really have.
var (
	rsyncUserRe   = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	rcloneDriveRe = regexp.MustCompile(`^[A-Za-z0-9_.+@][A-Za-z0-9_ .+@-]*$`)
)

func validateArchiveMountValues(cfg ArchiveConfig) error {
	// The configuration file is sourced by root's shell and read line by line
	// by other tools, so no value may contain a line break or control character.
	for _, value := range []string{
		cfg.System,
		cfg.CIFS.Server, cfg.CIFS.Share, cfg.CIFS.User, cfg.CIFS.Password, cfg.CIFS.Domain, cfg.CIFS.Version, cfg.CIFS.Sec,
		cfg.NFS.Server, cfg.NFS.Path,
		cfg.Rsync.User, cfg.Rsync.Server, cfg.Rsync.Path,
		cfg.Rclone.Drive, cfg.Rclone.Path,
	} {
		if hasControlChars(value) {
			return fmt.Errorf("archive settings must be single-line text without control characters")
		}
	}
	for _, value := range []string{cfg.CIFS.Server, cfg.NFS.Server, cfg.Rsync.Server} {
		if strings.ContainsAny(value, " \t\r\n\\/#\x00") || strings.HasPrefix(value, "-") {
			return fmt.Errorf("invalid archive server")
		}
	}
	if cfg.Rsync.User != "" && !rsyncUserRe.MatchString(cfg.Rsync.User) {
		return fmt.Errorf("invalid rsync user: use letters, digits, '.', '_' and '-' only")
	}
	if cfg.Rclone.Drive != "" && !rcloneDriveRe.MatchString(cfg.Rclone.Drive) {
		return fmt.Errorf("invalid rclone remote: use the remote's name without the colon")
	}
	for _, value := range []string{cfg.CIFS.Version, cfg.CIFS.Sec} {
		for _, char := range value {
			if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '_') {
				return fmt.Errorf("invalid CIFS version or security option")
			}
		}
	}
	return nil
}

func fstabEscape(value string) string {
	return strings.NewReplacer("\\", "\\134", " ", "\\040", "\t", "\\011", "#", "\\043").Replace(value)
}

func archiveFstab(content string, cfg ArchiveConfig, setup string) string {
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && !strings.HasPrefix(fields[0], "#") && (fields[1] == "/mnt/archive" || fields[1] == "/mnt/musicarchive") {
			continue
		}
		lines = append(lines, line)
	}
	appendMount := func(target, share, mode string) {
		if share == "" {
			return
		}
		var source, fs, options string
		switch cfg.System {
		case "cifs":
			source, fs = "//"+cfg.CIFS.Server+"/"+strings.TrimLeft(share, "/"), "cifs"
			options = mode + ",noauto,credentials=" + fstabEscape(cifsCredentialsFilePath) + ",iocharset=utf8,file_mode=0777,dir_mode=0777"
			if cfg.CIFS.Version != "" {
				options += ",vers=" + cfg.CIFS.Version
			}
			if cfg.CIFS.Sec != "" {
				options += ",sec=" + cfg.CIFS.Sec
			}
		case "nfs":
			source, fs = cfg.NFS.Server+":"+share, "nfs"
			options = mode + ",noauto,nolock,proto=tcp,vers=3"
		default:
			return
		}
		lines = append(lines, fmt.Sprintf("%s %s %s %s 0 0", fstabEscape(source), target, fs, options))
	}
	share := cfg.CIFS.Share
	if cfg.System == "nfs" {
		share = cfg.NFS.Path
	}
	appendMount("/mnt/archive", share, "rw")
	appendMount("/mnt/musicarchive", readConfKey(setup, "MUSIC_SHARE_NAME"), "ro")
	return strings.TrimLeft(strings.Join(lines, "\n"), "\n") + "\n"
}

// applyArchiveConfig runs inside withConfRW. The old destination is detached
// before any configuration changes, so it cannot receive new clips afterwards.
func applyArchiveConfig(cfg ArchiveConfig, content string) error {
	if err := validateArchiveMountValues(cfg); err != nil {
		return err
	}
	type fileState struct {
		path   string
		data   []byte
		mode   os.FileMode
		exists bool
	}
	var originals []fileState
	for _, path := range []string{confFilePath, archiveFstabPath, cifsCredentialsFilePath} {
		state := fileState{path: path}
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			state.data, state.mode, state.exists = data, info.Mode().Perm(), true
		}
		originals = append(originals, state)
	}
	if err := archiveReconfigure("prepare", cfg.System); err != nil {
		return err
	}
	restore := func(cause error) error {
		// Leave archiving stopped on write failure; don't restart against partial files.
		for _, state := range originals {
			var err error
			if state.exists {
				err = writeFileAtomic(state.path, state.data, state.mode)
			} else {
				err = os.Remove(state.path)
				if os.IsNotExist(err) {
					err = nil
				}
			}
			if err != nil {
				cause = errors.Join(cause, fmt.Errorf("restore %s: %w", state.path, err))
			}
		}
		return fmt.Errorf("archive configuration failed; archiving is stopped: %w", cause)
	}
	if cfg.System == "cifs" {
		creds := fmt.Sprintf("username=%s\npassword=%s\n", cfg.CIFS.User, cfg.CIFS.Password)
		if cfg.CIFS.Domain != "" {
			creds += "domain=" + cfg.CIFS.Domain + "\n"
		}
		if err := writeFileAtomic(cifsCredentialsFilePath, []byte(creds), 0o600); err != nil {
			return restore(err)
		}
	}
	if err := writeFileAtomic(archiveFstabPath, []byte(archiveFstab(string(originals[1].data), cfg, content)), 0o644); err != nil {
		return restore(err)
	}
	if err := writeFileAtomic(confFilePath, []byte(content), 0o600); err != nil {
		return restore(err)
	}
	// Keep the new files on restart failure. They describe the intended destination
	// and a subsequent restart must never silently fall back to the old one.
	if err := archiveReconfigure("restart"); err != nil {
		return err
	}
	return nil
}
