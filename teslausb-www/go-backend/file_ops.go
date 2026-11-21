package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// HandleLs lists directory contents in the specific format required by the teslausb frontend.
func HandleLs(w http.ResponseWriter, r *http.Request) {
	args := ParseURLArgs(r)
	if len(args) == 0 {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	docRoot := GetDocumentRoot()
	baseDir := filepath.Join(docRoot, args[0])
	cleanBase := filepath.Clean(baseDir)
	if !isWithin(cleanBase, docRoot) {
		http.Error(w, "Path traversal attempt", http.StatusBadRequest)
		return
	}

	lsPath := cleanBase
	if len(args) > 1 {
		subPath := filepath.Join(args[1:]...)
		lsPath = filepath.Join(cleanBase, subPath)
		if !isWithin(lsPath, cleanBase) {
			http.Error(w, "Path traversal attempt", http.StatusBadRequest)
			return
		}
	}

	w.Header().Set("Content-Type", "text/plain")

	var output []string

	// Walk dir depth 1 and 2
	entries, err := os.ReadDir(lsPath)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				output = append(output, fmt.Sprintf("d:%s", entry.Name()))
				// Depth 2 dirs
				subEntries, err := os.ReadDir(filepath.Join(lsPath, entry.Name()))
				if err == nil {
					for _, subEntry := range subEntries {
						if subEntry.IsDir() {
							output = append(output, fmt.Sprintf("D:%s/%s", entry.Name(), subEntry.Name()))
						}
					}
				}
			} else {
				info, err := entry.Info()
				if err == nil {
					output = append(output, fmt.Sprintf("f:%s:%d", entry.Name(), info.Size()))
				}
			}
		}
	}

	// Disk space
	var stat syscall.Statfs_t
	if err := syscall.Statfs(lsPath, &stat); err == nil {
		freeSpace := stat.Bfree * uint64(stat.Bsize)
		totalSpace := stat.Blocks * uint64(stat.Bsize)
		output = append(output, fmt.Sprintf("s:%d:%d", freeSpace, totalSpace))
	}

	// Sort case insensitive
	sort.Slice(output, func(i, j int) bool {
		return strings.ToLower(output[i]) < strings.ToLower(output[j])
	})

	for _, line := range output {
		fmt.Fprintln(w, line)
	}
}

func HandleDownload(w http.ResponseWriter, r *http.Request) {
	args := ParseURLArgs(r)
	if len(args) < 2 {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	safePath, err := GetSafePath(args)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	http.ServeFile(w, r, safePath)
}

func HandleDownloadZip(w http.ResponseWriter, r *http.Request) {
	args := ParseURLArgs(r)
	if len(args) < 1 {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	safePath, err := GetSafePath(args)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\"archive.zip\"")

	zw := zip.NewWriter(w)

	// wrote tracks whether any zip entry has been emitted yet; once the first
	// entry is created the response headers are already on the wire, so a late
	// failure can only be logged, not reported as an HTTP error.
	wrote := false
	walkErr := filepath.WalkDir(safePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(safePath, path)
		if err != nil {
			return err
		}
		f, err := zw.Create(relPath)
		if err != nil {
			return err
		}
		wrote = true
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		// Close explicitly per iteration — deferring here would keep every
		// clip file open until the whole zip finishes (FD exhaustion).
		_, copyErr := io.Copy(f, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if walkErr == nil {
		walkErr = zw.Close()
	}
	if walkErr != nil {
		if !wrote {
			http.Error(w, "Error creating archive", http.StatusInternalServerError)
			return
		}
		// Headers are already sent mid-zip — abort the stream and log.
		fmt.Fprintf(os.Stderr, "warning: zip of %s aborted: %v\n", safePath, walkErr)
	}
}

func HandleUpload(w http.ResponseWriter, r *http.Request) {
	// The bash upload.sh logic saves stream directly to file via `cat > "$file"`
	// Actually teslausb frontend uses a POST request with the file content.
	args := ParseURLArgs(r)
	if len(args) < 2 {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	safePath, err := GetSafePath(args)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	limit := uploadLimitBytes()
	r.Body = http.MaxBytesReader(w, r.Body, limit)

	// Write to a sibling temp file first; rename into place only on full success.
	// This guarantees the destination either contains the previous version (if
	// the upload was an overwrite) or the complete new payload — never a
	// truncated mix. os.CreateTemp picks a unique name so concurrent uploads
	// to the same destination don't trample each other before the final rename.
	dir := filepath.Dir(safePath)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(safePath)+".upload-*")
	if err != nil {
		http.Error(w, "Error creating file", http.StatusInternalServerError)
		return
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	if _, err := io.Copy(tmp, r.Body); err != nil {
		cleanup()
		if _, ok := err.(*http.MaxBytesError); ok || strings.Contains(err.Error(), "http: request body too large") {
			writeJSONError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("upload exceeds %d byte limit", limit))
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "upload failed")
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		writeJSONError(w, http.StatusInternalServerError, "upload failed")
		return
	}
	if err := os.Rename(tmpPath, safePath); err != nil {
		_ = os.Remove(tmpPath)
		writeJSONError(w, http.StatusInternalServerError, "upload failed")
		return
	}
	fmt.Fprintln(w, "OK")
}

func HandleRm(w http.ResponseWriter, r *http.Request) {
	args := ParseURLArgs(r)
	// ParseURLArgs returns [""] for an empty query string, which would
	// resolve to the document root itself — reject blank paths outright.
	hasPath := false
	for _, a := range args {
		if strings.TrimSpace(a) != "" {
			hasPath = true
			break
		}
	}
	if !hasPath {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	safePath, err := GetSafePath(args)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	if safePath == filepath.Clean(GetDocumentRoot()) {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if err := os.RemoveAll(safePath); err != nil {
		http.Error(w, "Delete failed", http.StatusInternalServerError)
		return
	}
	fmt.Fprintln(w, "OK")
}

func HandleMkdir(w http.ResponseWriter, r *http.Request) {
	args := ParseURLArgs(r)
	safePath, err := GetSafePath(args)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if err := os.MkdirAll(safePath, 0755); err != nil {
		http.Error(w, "Mkdir failed", http.StatusInternalServerError)
		return
	}
	fmt.Fprintln(w, "OK")
}

func HandleCp(w http.ResponseWriter, r *http.Request) {
	// cp.sh uses paths separated by &
	// e.g. cp.sh?TeslaCam/&src&dest
	args := ParseURLArgs(r)
	if len(args) < 3 {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	src, err := GetSafePath(args[:2])
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	dest, err := GetSafePath([]string{args[0], args[2]})
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Just use cp command for simplicity
	cmd := exec.Command("cp", "-r", src, dest)
	if err := cmd.Run(); err != nil {
		http.Error(w, "Copy failed", http.StatusInternalServerError)
		return
	}
	fmt.Fprintln(w, "OK")
}

func HandleMv(w http.ResponseWriter, r *http.Request) {
	args := ParseURLArgs(r)
	if len(args) < 3 {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	src, err := GetSafePath(args[:2])
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	dest, err := GetSafePath([]string{args[0], args[2]})
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if err := os.Rename(src, dest); err != nil {
		// TeslaCam and fs are separate mountpoints; a cross-mount rename
		// fails with EXDEV — fall back to copy + delete.
		if !errors.Is(err, syscall.EXDEV) {
			http.Error(w, "Move failed", http.StatusInternalServerError)
			return
		}
		if err := exec.Command("cp", "-r", src, dest).Run(); err != nil {
			http.Error(w, "Move failed", http.StatusInternalServerError)
			return
		}
		if err := os.RemoveAll(src); err != nil {
			http.Error(w, "Move failed", http.StatusInternalServerError)
			return
		}
	}
	fmt.Fprintln(w, "OK")
}
