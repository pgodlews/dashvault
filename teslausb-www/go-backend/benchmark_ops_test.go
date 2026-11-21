package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// redirectBenchmarkPaths redirects all benchmark-related file paths to a
// temporary directory and restores the originals via t.Cleanup.
func redirectBenchmarkPaths(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()

	orig := struct {
		rf, ws, bs, si, ms, as, tf, sentinel string
	}{
		benchmarkResultsFile, benchmarkWriteStats, benchmarkScript,
		sdcardInfoScript, mmcBlockStatPath, archiveStateFile,
		tbwThresholdFile, tbwWarnedSentinel,
	}

	benchmarkResultsFile = filepath.Join(tmp, "sdcard_benchmark.json")
	benchmarkWriteStats = filepath.Join(tmp, "sdcard_write_stats.json")
	benchmarkScript = filepath.Join(tmp, "benchmark.sh")
	sdcardInfoScript = filepath.Join(tmp, "sdcard_info.sh")
	mmcBlockStatPath = filepath.Join(tmp, "mmcblk0_stat")
	archiveStateFile = filepath.Join(tmp, "archive_state")
	tbwThresholdFile = filepath.Join(tmp, "sdcard_tbw_threshold")
	tbwWarnedSentinel = filepath.Join(tmp, "sdcard_tbw_warned")

	t.Cleanup(func() {
		benchmarkResultsFile = orig.rf
		benchmarkWriteStats = orig.ws
		benchmarkScript = orig.bs
		sdcardInfoScript = orig.si
		mmcBlockStatPath = orig.ms
		archiveStateFile = orig.as
		tbwThresholdFile = orig.tf
		tbwWarnedSentinel = orig.sentinel
	})
	return tmp
}

// ── DefaultTBWThresholdTB ──────────────────────────────────────────────────────

func TestDefaultTBWThresholdTB_known(t *testing.T) {
	tests := []struct {
		capacityGB float64
		wantTB     float64
	}{
		{32, 16.0},
		{64, 32.0},
		{128, 64.0},
		{256, 128.0},
	}
	for _, tc := range tests {
		got := DefaultTBWThresholdTB(tc.capacityGB)
		if got != tc.wantTB {
			t.Errorf("DefaultTBWThresholdTB(%v) = %v, want %v", tc.capacityGB, got, tc.wantTB)
		}
	}
}

func TestDefaultTBWThresholdTB_zero(t *testing.T) {
	// Fallback when capacity is unknown.
	got := DefaultTBWThresholdTB(0)
	if got != 32.0 {
		t.Errorf("want 32.0 fallback, got %v", got)
	}
}

func TestDefaultTBWThresholdTB_minimum(t *testing.T) {
	// Very small cards (< 2 GB) should not give a threshold below 1 TB.
	got := DefaultTBWThresholdTB(1)
	if got < 1.0 {
		t.Errorf("minimum threshold should be 1.0 TB, got %v", got)
	}
}

// ── readTBWThresholdTB / writeTBWThresholdTB ───────────────────────────────────

func TestTBWThreshold_roundtrip(t *testing.T) {
	redirectBenchmarkPaths(t)

	// No file yet → default.
	got := readTBWThresholdTB(64)
	if got != DefaultTBWThresholdTB(64) {
		t.Errorf("want default %.1f, got %.1f", DefaultTBWThresholdTB(64), got)
	}

	// Write a custom threshold.
	if err := writeTBWThresholdTB(50.0); err != nil {
		t.Fatal(err)
	}
	got = readTBWThresholdTB(64)
	if got != 50.0 {
		t.Errorf("want 50.0, got %v", got)
	}
}

// ── isTBWWarning ──────────────────────────────────────────────────────────────

func TestIsTBWWarning(t *testing.T) {
	const oneTB = int64(1e12)
	if isTBWWarning(30*oneTB, 32.0) {
		t.Error("30 TB < 32 TB threshold should not warn")
	}
	if !isTBWWarning(32*oneTB, 32.0) {
		t.Error("32 TB == 32 TB threshold should warn")
	}
	if !isTBWWarning(40*oneTB, 32.0) {
		t.Error("40 TB > 32 TB threshold should warn")
	}
	// Zero bytes written should never warn.
	if isTBWWarning(0, 32.0) {
		t.Error("zero bytes should not warn")
	}
}

// ── HandleBenchmarkThreshold ──────────────────────────────────────────────────

func TestHandleBenchmarkThreshold_get(t *testing.T) {
	redirectBenchmarkPaths(t)

	req := httptest.NewRequest(http.MethodGet, "/api/benchmark/threshold", nil)
	w := httptest.NewRecorder()
	HandleBenchmarkThreshold(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if _, ok := resp["threshold_tb"]; !ok {
		t.Error("response missing threshold_tb")
	}
	if _, ok := resp["default_threshold_tb"]; !ok {
		t.Error("response missing default_threshold_tb")
	}
}

func TestHandleBenchmarkThreshold_post(t *testing.T) {
	redirectBenchmarkPaths(t)

	body := strings.NewReader(`{"threshold_tb": 42.5}`)
	req := httptest.NewRequest(http.MethodPost, "/api/benchmark/threshold", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	HandleBenchmarkThreshold(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]float64
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["threshold_tb"] != 42.5 {
		t.Errorf("want threshold_tb=42.5, got %v", resp["threshold_tb"])
	}

	// Verify it was persisted.
	raw, err := os.ReadFile(tbwThresholdFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "42.50") {
		t.Errorf("persisted file content unexpected: %s", raw)
	}
}

func TestHandleBenchmarkThreshold_invalidReturns400(t *testing.T) {
	redirectBenchmarkPaths(t)

	body := strings.NewReader(`{"threshold_tb": -1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/benchmark/threshold", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	HandleBenchmarkThreshold(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

// ── isArchiving ───────────────────────────────────────────────────────────────

func TestIsArchiving(t *testing.T) {
	redirectBenchmarkPaths(t)

	// No file → not archiving.
	if isArchiving() {
		t.Error("no state file should not be archiving")
	}

	// Write "idle".
	if err := os.WriteFile(archiveStateFile, []byte("idle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isArchiving() {
		t.Error("idle state should not be archiving")
	}

	// Write "archiving: copying clips".
	if err := os.WriteFile(archiveStateFile, []byte("archiving: copying clips\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isArchiving() {
		t.Error("expected archiving=true")
	}
}

// ── HandleBenchmarkRun blocks when archiving ───────────────────────────────────

func TestHandleBenchmarkRun_blockedWhileArchiving(t *testing.T) {
	redirectBenchmarkPaths(t)

	// Simulate an active archive.
	if err := os.WriteFile(archiveStateFile, []byte("archiving: copying clips"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/benchmark/run", nil)
	w := httptest.NewRecorder()
	HandleBenchmarkRun(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("want 409 Conflict while archiving, got %d: %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "archive is running") {
		t.Errorf("expected descriptive error, got: %s", w.Body)
	}
}

// ── HandleBenchmarkStatus includes warning fields ─────────────────────────────

func TestHandleBenchmarkStatus_writeWarning(t *testing.T) {
	redirectBenchmarkPaths(t)

	// Write stats showing 50 TB written.
	stats := WriteStats{TotalBytesWritten: int64(50e12), LastSectors: 1, LastUpdated: "2024-01-01T00:00:00Z", TrackingSince: "2023-01-01T00:00:00Z"}
	raw, _ := json.Marshal(stats)
	if err := os.WriteFile(benchmarkWriteStats, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	// Set threshold to 32 TB → 50 TB should trigger warning.
	if err := writeTBWThresholdTB(32.0); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/benchmark/status", nil)
	w := httptest.NewRecorder()
	HandleBenchmarkStatus(w, req)

	var resp BenchmarkStatus
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.WriteWarning {
		t.Error("expected WriteWarning=true when written TB exceeds threshold")
	}
	if resp.ThresholdTB != 32.0 {
		t.Errorf("want ThresholdTB=32.0, got %v", resp.ThresholdTB)
	}
}

func TestHandleBenchmarkStatus_noWarning(t *testing.T) {
	redirectBenchmarkPaths(t)

	// Write stats showing 5 TB written — well below any threshold.
	stats := WriteStats{TotalBytesWritten: int64(5e12), LastSectors: 1, LastUpdated: "2024-01-01T00:00:00Z", TrackingSince: "2023-01-01T00:00:00Z"}
	raw, _ := json.Marshal(stats)
	if err := os.WriteFile(benchmarkWriteStats, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/benchmark/status", nil)
	w := httptest.NewRecorder()
	HandleBenchmarkStatus(w, req)

	var resp BenchmarkStatus
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.WriteWarning {
		t.Error("expected WriteWarning=false for 5 TB with default threshold")
	}
}

// ── write stats are kept in memory and persisted rarely ──────────────────────

func TestUpdateWriteStats_persistsAtMostOncePerInterval(t *testing.T) {
	redirectBenchmarkPaths(t)
	origInterval := writeStatsPersistInterval
	t.Cleanup(func() { writeStatsPersistInterval = origInterval })
	writeStatsPersistInterval = time.Hour

	tick := func(sectors int64) {
		t.Helper()
		line := fmt.Sprintf("1 0 0 0 2 0 %d 0 0 0 0\n", sectors)
		if err := os.WriteFile(mmcBlockStatPath, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		UpdateWriteStats()
	}
	onDisk := func() WriteStats {
		t.Helper()
		raw, err := os.ReadFile(benchmarkWriteStats)
		if err != nil {
			t.Fatal(err)
		}
		var stats WriteStats
		if err := json.Unmarshal(raw, &stats); err != nil {
			t.Fatal(err)
		}
		return stats
	}

	// The first tick creates the file so tracking_since survives a power cut.
	tick(1000)
	if onDisk().TrackingSince == "" {
		t.Fatal("first tick did not persist tracking_since")
	}

	// Later ticks inside the interval count in memory but do not touch the card.
	tick(3000)
	tick(5000)
	if got := readWriteStats(); got == nil || got.TotalBytesWritten != 4000*512 {
		t.Fatalf("live stats = %+v, want %d bytes", got, 4000*512)
	}
	if got := onDisk().TotalBytesWritten; got != 0 {
		t.Fatalf("persisted %d bytes inside the interval, want 0", got)
	}

	// Once the interval has passed the total is written.
	writeStatsPersistInterval = 0
	tick(6000)
	if got := onDisk().TotalBytesWritten; got != 5000*512 {
		t.Fatalf("persisted %d bytes after the interval, want %d", got, 5000*512)
	}

	// A backend restart (empty cache) continues from the persisted counters.
	writeStatsPersistInterval = time.Hour
	writeStatsCache.Lock()
	writeStatsCache.path = ""
	writeStatsCache.Unlock()
	tick(6500)
	if got := readWriteStats(); got == nil || got.TotalBytesWritten != 5500*512 {
		t.Fatalf("stats after restart = %+v, want %d bytes", got, 5500*512)
	}
}

// ── trySetBenchmarkRunning is atomic ─────────────────────────────────────────

func TestTrySetBenchmarkRunning(t *testing.T) {
	t.Cleanup(func() { setBenchmarkRunning(false) })

	if !trySetBenchmarkRunning() {
		t.Fatal("first trySetBenchmarkRunning should succeed")
	}
	if trySetBenchmarkRunning() {
		t.Error("second trySetBenchmarkRunning should fail while running")
	}
	setBenchmarkRunning(false)

	// Concurrent callers: exactly one wins.
	const n = 16
	var wg sync.WaitGroup
	wins := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wins <- trySetBenchmarkRunning()
		}()
	}
	wg.Wait()
	close(wins)
	count := 0
	for won := range wins {
		if won {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 concurrent winner, got %d", count)
	}

	setBenchmarkRunning(false)
	if !trySetBenchmarkRunning() {
		t.Error("trySetBenchmarkRunning should succeed again after reset")
	}
}

// ── HandleBenchmarkRun blocks when already running ───────────────────────────

func TestHandleBenchmarkRun_blockedWhileRunning(t *testing.T) {
	redirectBenchmarkPaths(t)

	if !trySetBenchmarkRunning() {
		t.Fatal("expected to acquire the benchmark slot")
	}
	t.Cleanup(func() { setBenchmarkRunning(false) })

	req := httptest.NewRequest(http.MethodPost, "/api/benchmark/run", nil)
	w := httptest.NewRecorder()
	HandleBenchmarkRun(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("want 409 Conflict while running, got %d: %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "benchmark already running") {
		t.Errorf("expected descriptive error, got: %s", w.Body)
	}
}
