package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadUptime(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "proc", "uptime"), "1234567.89 2345678.90\n")

	c := &Collector{ProcRoot: dir}
	got := c.readUptime()
	if got != 1234567.89 {
		t.Errorf("uptime: got %v, want 1234567.89", got)
	}
}

func TestReadUptimeMissing(t *testing.T) {
	c := &Collector{ProcRoot: t.TempDir()}
	if got := c.readUptime(); got != 0 {
		t.Errorf("missing file: want 0, got %v", got)
	}
}

func TestReadCPUTemp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sys", "class", "thermal", "thermal_zone0", "temp"), "46200\n")

	c := &Collector{ProcRoot: dir}
	got := c.readCPUTemp()
	if got != 46.2 {
		t.Errorf("cpu temp: got %v, want 46.2", got)
	}
}

func TestReadCPUTempMissing(t *testing.T) {
	c := &Collector{ProcRoot: t.TempDir()}
	if got := c.readCPUTemp(); got != 0 {
		t.Errorf("missing file: want 0, got %v", got)
	}
}

func TestReadArchiveUnknown(t *testing.T) {
	c := &Collector{ArchiveFile: filepath.Join(t.TempDir(), "archive_state")}
	s := c.readArchive()
	if s.State != "unknown" {
		t.Errorf("want unknown, got %q", s.State)
	}
}

func TestReadArchiveIdle(t *testing.T) {
	f := filepath.Join(t.TempDir(), "archive_state")
	writeFile(t, f, "idle\n")
	c := &Collector{ArchiveFile: f}
	s := c.readArchive()
	if s.State != "idle" {
		t.Errorf("want idle, got %q", s.State)
	}
	if s.Message != "" {
		t.Errorf("want empty message, got %q", s.Message)
	}
}

func TestReadArchiveArchiving(t *testing.T) {
	f := filepath.Join(t.TempDir(), "archive_state")
	writeFile(t, f, "archiving: 14.2 GB copied\n")
	c := &Collector{ArchiveFile: f}
	s := c.readArchive()
	if s.State != "archiving" {
		t.Errorf("want archiving, got %q", s.State)
	}
	if s.Message != "14.2 GB copied" {
		t.Errorf("want '14.2 GB copied', got %q", s.Message)
	}
}

func TestReadArchiveError(t *testing.T) {
	f := filepath.Join(t.TempDir(), "archive_state")
	writeFile(t, f, "error: mount failed\n")
	c := &Collector{ArchiveFile: f}
	s := c.readArchive()
	if s.State != "error" {
		t.Errorf("want error, got %q", s.State)
	}
	if s.Message != "mount failed" {
		t.Errorf("want 'mount failed', got %q", s.Message)
	}
}

func TestReadWiFiMissing(t *testing.T) {
	c := &Collector{ProcRoot: t.TempDir()}
	if w := c.readWiFi(); w != nil {
		t.Errorf("want nil WiFiInfo when /proc/net/wireless missing, got %+v", w)
	}
}

func TestReadWiFiParsed(t *testing.T) {
	dir := t.TempDir()
	content := `Inter-| sta-|   Quality        |   Discarded packets               | Missed | WE
 face | tus | link level noise |  nwid  crypt   frag  retry   misc | beacon | 22
 wlan0: 0000   60.  -49.  -95.   0.     0.     0.     3.    0.      0
`
	writeFile(t, filepath.Join(dir, "proc", "net", "wireless"), content)
	c := &Collector{ProcRoot: dir}
	w := c.readWiFi()
	if w == nil {
		t.Fatal("want WiFiInfo, got nil")
	}
	if w.LinkLevel != 60 {
		t.Errorf("link: want 60, got %d", w.LinkLevel)
	}
	if w.SignalDBm != -49 {
		t.Errorf("signal: want -49, got %d", w.SignalDBm)
	}
	if w.NoiseDBm != -95 {
		t.Errorf("noise: want -95, got %d", w.NoiseDBm)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
