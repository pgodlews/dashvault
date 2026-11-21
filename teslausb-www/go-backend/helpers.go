package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// defaultUploadLimitMB is the fallback upload body cap (2 GiB) when
// TESLAUSB_MAX_UPLOAD_MB is unset. Matches the nginx client_max_body_size.
const defaultUploadLimitMB = 2048

// uploadLimitBytes returns the configured upload body cap in bytes.
// Honours TESLAUSB_MAX_UPLOAD_MB (>= 1); otherwise 2 GiB.
func uploadLimitBytes() int64 {
	if v := os.Getenv("TESLAUSB_MAX_UPLOAD_MB"); v != "" {
		if mb, err := strconv.ParseInt(v, 10, 64); err == nil && mb > 0 {
			return mb * 1024 * 1024
		}
	}
	return int64(defaultUploadLimitMB) * 1024 * 1024
}

// writeJSONError emits a small JSON error body the new UI can render.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

var rootFSLockPath = "/run/teslausb/rootfs.lock"

func withRootFSLock(fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(rootFSLockPath), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(rootFSLockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }()
	return fn()
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".teslausb-write-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	defer file.Close()
	if err := file.Chmod(perm); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// GetDocumentRoot returns the base document root (usually /var/www/html or /mutable)
func GetDocumentRoot() string {
	docRoot := os.Getenv("DOCUMENT_ROOT")
	if docRoot == "" {
		docRoot = "/var/www/html" // Default fallback
	}
	return docRoot
}

// ParseURLArgs parses the weird query string format used by teslausb's frontend (e.g. ?base/&subpath)
func ParseURLArgs(r *http.Request) []string {
	rawQuery := r.URL.RawQuery
	parts := strings.Split(rawQuery, "&")
	args := make([]string, len(parts))
	for i, p := range parts {
		// Replace '+' with space first
		p = strings.ReplaceAll(p, "+", " ")
		// URL decode
		decoded, err := url.QueryUnescape(p)
		if err == nil {
			args[i] = decoded
		} else {
			args[i] = p
		}
	}
	return args
}

// GetSafePath joins the args and ensures they don't escape the document root.
func GetSafePath(args []string) (string, error) {
	docRoot := GetDocumentRoot()
	if len(args) == 0 {
		return "", fmt.Errorf("no path arguments provided")
	}

	joined := filepath.Join(args...)
	fullPath := filepath.Join(docRoot, joined)
	cleanPath := filepath.Clean(fullPath)

	if !isWithin(cleanPath, docRoot) {
		return "", fmt.Errorf("path traversal attempt detected")
	}

	return cleanPath, nil
}

// isWithin reports whether path is equal to or nested under base. It guards
// against the prefix-string trap where "/var/www/html" matches "/var/www/html-evil"
// by requiring a path-separator boundary after the base.
func isWithin(path, base string) bool {
	cleanBase := filepath.Clean(base)
	cleanPath := filepath.Clean(path)
	if cleanPath == cleanBase {
		return true
	}
	return strings.HasPrefix(cleanPath, cleanBase+string(os.PathSeparator))
}
