package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
)

// logSource describes how to fetch logs for one UI tab.
type logSource struct {
	// For journalctl-based sources: unit name ("" = all units, "kernel" = -k flag).
	journalUnit string
	isKernel    bool // use journalctl -k instead of -u

	// For file-based sources: tail these files instead of journalctl.
	files []string
}

// knownSources maps the ?unit= query param to a logSource.
// Only these keys are accepted; unknown keys fall back to allUnits.
var knownSources = map[string]logSource{
	"backend": {journalUnit: "teslausb-backend"},
	"archive": {journalUnit: "teslausb"}, // archiveloop runs inside teslausb.service
	"nginx":   {files: []string{"/var/log/nginx/error.log", "/var/log/nginx/access.log"}},
	"kernel":  {isKernel: true},
}

var allUnits = logSource{} // journalctl with no -u filter

const (
	defaultLogLines = 200
	maxLogLines     = 2000
)

// parseLogParams validates query params and returns the resolved source + line count.
func parseLogParams(r *http.Request) (logSource, int) {
	nLines := defaultLogLines
	if n, err := strconv.Atoi(r.URL.Query().Get("lines")); err == nil && n > 0 {
		if n > maxLogLines {
			n = maxLogLines
		}
		nLines = n
	}
	key := r.URL.Query().Get("unit")
	src, ok := knownSources[key]
	if !ok {
		src = allUnits
	}
	return src, nLines
}

// snapshotCmd returns a command that prints the last nLines of logs and exits.
// The command is bound to ctx so a client disconnect cancels it.
func (s logSource) snapshotCmd(ctx context.Context, nLines int) *exec.Cmd {
	if len(s.files) > 0 {
		args := append([]string{"-n", strconv.Itoa(nLines)}, s.files...)
		return exec.CommandContext(ctx, "tail", args...)
	}
	args := []string{"--no-pager", "--output", "short-iso", "-n", strconv.Itoa(nLines)}
	if s.isKernel {
		args = append(args, "-k")
	} else if s.journalUnit != "" {
		args = append(args, "-u", s.journalUnit)
	}
	return exec.CommandContext(ctx, "journalctl", args...)
}

// streamCmd returns a command that follows and streams new log lines.
func (s logSource) streamCmd(nLines int) *exec.Cmd {
	if len(s.files) > 0 {
		// tail -F follows across log rotations; -n shows recent context first.
		args := append([]string{"-F", "-n", strconv.Itoa(nLines)}, s.files...)
		return exec.Command("tail", args...)
	}
	args := []string{"--no-pager", "--output", "short-iso", "-n", strconv.Itoa(nLines), "-f"}
	if s.isKernel {
		args = append(args, "-k")
	} else if s.journalUnit != "" {
		args = append(args, "-u", s.journalUnit)
	}
	return exec.Command("journalctl", args...)
}

// HandleLogSnapshot — GET /api/logs
// Returns the last N lines as a JSON array of strings.
func HandleLogSnapshot(w http.ResponseWriter, r *http.Request) {
	src, nLines := parseLogParams(r)
	cmd := src.snapshotCmd(r.Context(), nLines)
	out, err := cmd.Output()

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.Write([]byte("[]\n"))
		return
	}
	_ = json.NewEncoder(w).Encode(splitLines(string(out)))
}

// HandleLogStream — GET /api/logs/stream
// Streams new log lines as SSE events; each event carries one log line as a
// JSON-encoded string so the client doesn't have to worry about escaping.
func HandleLogStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	src, nLines := parseLogParams(r)
	cmd := src.streamCmd(nLines)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, "pipe error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if err := cmd.Start(); err != nil {
		writeLogEvent(w, "[log source unavailable]")
		flusher.Flush()
		return
	}
	// journalctl -f / tail -F block forever when no new lines arrive, so the
	// scanner loop below would never notice a client disconnect on its own.
	// Kill the child as soon as the request context is done.
	stop := context.AfterFunc(r.Context(), func() { _ = cmd.Process.Kill() })
	defer stop()
	// Kill (in case the loop exited early) then Wait to reap the child —
	// Kill alone leaves a zombie process behind.
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		if err := writeLogEvent(w, sc.Text()); err != nil {
			return
		}
		flusher.Flush()
	}
}

func writeLogEvent(w http.ResponseWriter, line string) error {
	encoded, _ := json.Marshal(line) // JSON-encode so newlines/quotes are safe
	_, err := fmt.Fprintf(w, "data: %s\n\n", encoded)
	return err
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if line := s[start:i]; line != "" {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		if line := s[start:]; line != "" {
			out = append(out, line)
		}
	}
	return out
}
