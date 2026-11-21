package main

// benchmark_ops.go — microSD card benchmark and write-tracking API.
//
// API surface:
//   GET  /api/benchmark/status  — last 3 results + card info + write stats + running flag
//   POST /api/benchmark/run     — start benchmark, SSE progress stream

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── path constants (vars so tests can redirect) ────────────────────────────────

var (
	benchmarkResultsFile = "/mutable/sdcard_benchmark.json"
	benchmarkWriteStats  = "/mutable/sdcard_write_stats.json"
	benchmarkScript      = "/root/bin/benchmark.sh"
	sdcardInfoScript     = "/root/bin/sdcard_info.sh"
	mmcBlockStatPath     = "" // empty: /sys/block/<boot disk>/stat

	// archiveStateFile is the file archiveloop writes its current state to.
	// Shared with collect.go's Collector.ArchiveFile default.
	archiveStateFile = "/run/teslausb/archive_state"

	// TBW (Total Bytes Written) endurance warning.
	tbwThresholdFile  = "/mutable/sdcard_tbw_threshold"
	tbwWarnedSentinel = "/mutable/sdcard_tbw_warned"
)

// ── types ──────────────────────────────────────────────────────────────────────

// BenchmarkResult is a single benchmark run.
type BenchmarkResult struct {
	Timestamp string   `json:"timestamp"`
	WriteMBps *float64 `json:"write_mbps"` // nil if timed out
	Rating    string   `json:"rating"`     // "good" | "marginal" | "slow" | "unknown"
	TimedOut  bool     `json:"timed_out"`
	SizeMB    int      `json:"size_mb"`
	ElapsedMs int64    `json:"elapsed_ms"`
	Error     string   `json:"error,omitempty"`
}

// SdCardInfo holds card identity from sysfs.
type SdCardInfo struct {
	Available    bool    `json:"available"`
	Name         string  `json:"name,omitempty"`
	Manufacturer string  `json:"manufacturer,omitempty"`
	ManfID       string  `json:"manfid,omitempty"`
	Serial       string  `json:"serial,omitempty"`
	Date         string  `json:"date,omitempty"`
	CapacityGB   float64 `json:"capacity_gb,omitempty"`
}

// WriteStats tracks cumulative bytes written to the SD card.
type WriteStats struct {
	TotalBytesWritten int64  `json:"total_bytes_written"`
	LastSectors       int64  `json:"last_sectors"`
	LastUpdated       string `json:"last_updated"`
	TrackingSince     string `json:"tracking_since"`
}

// BenchmarkStatus is returned by GET /api/benchmark/status.
type BenchmarkStatus struct {
	Running            bool              `json:"running"`
	Results            []BenchmarkResult `json:"results"` // last 3, newest first
	CardInfo           *SdCardInfo       `json:"card_info,omitempty"`
	WriteStats         *WriteStats       `json:"write_stats,omitempty"`
	LatestRating       string            `json:"latest_rating,omitempty"` // convenience
	WriteWarning       bool              `json:"write_warning"`
	ThresholdTB        float64           `json:"threshold_tb"`
	DefaultThresholdTB float64           `json:"default_threshold_tb"`
}

// ── running state ──────────────────────────────────────────────────────────────

var benchmarkMu sync.Mutex
var benchmarkRunning bool

func setBenchmarkRunning(v bool) {
	benchmarkMu.Lock()
	benchmarkRunning = v
	benchmarkMu.Unlock()
}

func isBenchmarkRunning() bool {
	benchmarkMu.Lock()
	defer benchmarkMu.Unlock()
	return benchmarkRunning
}

// trySetBenchmarkRunning atomically marks the benchmark as running and
// returns true, or returns false when one is already in progress. A caller
// that receives true must call setBenchmarkRunning(false) when done.
func trySetBenchmarkRunning() bool {
	benchmarkMu.Lock()
	defer benchmarkMu.Unlock()
	if benchmarkRunning {
		return false
	}
	benchmarkRunning = true
	return true
}

// isArchiving returns true when archiveloop is actively copying clips.
// The benchmark must not run concurrently — I/O contention would skew results
// and could slow down an active archive session.
func isArchiving() bool {
	data, err := os.ReadFile(archiveStateFile)
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(data)), "archiving")
}

// ── helpers ────────────────────────────────────────────────────────────────────

// readBenchmarkResults loads the stored results file.
func readBenchmarkResults() []BenchmarkResult {
	data, err := os.ReadFile(benchmarkResultsFile)
	if err != nil {
		return nil
	}
	var stored struct {
		Results []BenchmarkResult `json:"results"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil
	}
	return stored.Results
}

// readSdCardInfo runs sdcard_info.sh and returns parsed output, falling back
// to sysfs when the helper script is absent or fails.
func readSdCardInfo() *SdCardInfo {
	if _, err := os.Stat(sdcardInfoScript); err != nil {
		return readSdCardInfoSysfs()
	}
	out, err := exec.Command(sdcardInfoScript).Output()
	if err != nil {
		return readSdCardInfoSysfs()
	}
	var info SdCardInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return readSdCardInfoSysfs()
	}
	return &info
}

// readSdCardInfoSysfs reads card identity directly when the script is absent.
// The card is the boot disk's device (an SD card or eMMC), whatever its
// mmc host number.
func readSdCardInfoSysfs() *SdCardInfo {
	base := bootDiskSysfs("device")
	readStr := func(name string) string {
		b, err := os.ReadFile(base + "/" + name)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	if readStr("name") == "" && readStr("manfid") == "" {
		return &SdCardInfo{Available: false}
	}

	info := &SdCardInfo{
		Available:    true,
		Name:         readStr("name"),
		Manufacturer: manufacturerName(readStr("manfid")),
		ManfID:       readStr("manfid"),
		Serial:       readStr("serial"),
		Date:         readStr("date"),
	}

	if b, err := os.ReadFile(bootDiskSysfs("size")); err == nil {
		if sectors, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil {
			info.CapacityGB = float64(sectors*512) / 1e9
		}
	}
	return info
}

var manfIDMap = map[string]string{
	"0x000001": "Panasonic",
	"0x000002": "Toshiba",
	"0x000003": "SanDisk",
	"0x00001b": "Samsung",
	"0x00001d": "AData",
	"0x000027": "Phison",
	"0x000028": "Lexar",
	"0x000031": "Silicon Power",
	"0x000041": "Kingston",
	"0x000074": "Transcend",
	"0x000082": "Sony",
	"0x000090": "SK Hynix",
}

func manufacturerName(id string) string {
	if name, ok := manfIDMap[id]; ok {
		return name
	}
	return "Unknown"
}

// ── write tracking ─────────────────────────────────────────────────────────────

// writeStatsPersistInterval limits how often the running total is written to
// the SD card. The counter is updated in memory on every collector tick (2 s);
// persisting each tick would cost tens of thousands of fsyncs a day on the very
// card whose wear is being tracked. A power cut loses at most this much
// accounting. A var so tests can shorten it.
var writeStatsPersistInterval = 10 * time.Minute

// writeStatsCache holds the live counters between persists.
var writeStatsCache struct {
	sync.Mutex
	path        string // benchmarkWriteStats the cache was loaded from
	stats       WriteStats
	persisted   int64 // TotalBytesWritten at the last persist
	persistedAt time.Time
}

// UpdateWriteStats reads the boot disk's /sys/block/<disk>/stat, calculates
// the delta since the last call, and accumulates the total bytes written.
// Called by the status collector on each tick; the total is kept in memory and
// written to benchmarkWriteStats at most once per writeStatsPersistInterval.
func UpdateWriteStats() {
	statPath := mmcBlockStatPath
	if statPath == "" {
		statPath = bootDiskSysfs("stat")
	}
	data, err := os.ReadFile(statPath)
	if err != nil {
		return // not a Pi or not an SD card — silently skip
	}

	fields := strings.Fields(strings.TrimSpace(string(data)))
	if len(fields) < 8 {
		return
	}
	// Field index 6 (0-based) = sectors written (512 bytes each).
	currentSectors, err := strconv.ParseInt(fields[6], 10, 64)
	if err != nil {
		return
	}

	c := &writeStatsCache
	c.Lock()
	defer c.Unlock()

	// Load the persisted stats once (again if a test redirected the path).
	if c.path != benchmarkWriteStats {
		c.path = benchmarkWriteStats
		c.stats = WriteStats{}
		c.persistedAt = time.Time{}
		if raw, err := os.ReadFile(benchmarkWriteStats); err == nil {
			_ = json.Unmarshal(raw, &c.stats)
		}
		c.persisted = c.stats.TotalBytesWritten
	}

	now := time.Now()
	stamp := now.UTC().Format(time.RFC3339)

	firstRun := c.stats.TrackingSince == ""
	if firstRun {
		c.stats.TrackingSince = stamp
	}
	if c.stats.LastSectors > 0 && currentSectors > c.stats.LastSectors {
		delta := currentSectors - c.stats.LastSectors
		c.stats.TotalBytesWritten += delta * 512
	}
	c.stats.LastSectors = currentSectors
	c.stats.LastUpdated = stamp

	changed := c.stats.TotalBytesWritten != c.persisted
	due := changed && now.Sub(c.persistedAt) >= writeStatsPersistInterval
	if !firstRun && !due {
		return
	}
	// Mark the attempt even if it fails, so a missing or read-only /mutable
	// is retried once per interval rather than on every tick.
	c.persistedAt = now
	if raw, err := json.Marshal(c.stats); err == nil {
		if writeFileAtomic(benchmarkWriteStats, raw, 0o644) == nil {
			c.persisted = c.stats.TotalBytesWritten
		}
	}
}

// readWriteStats returns the live write stats when the collector is tracking
// them, otherwise the persisted ones.
func readWriteStats() *WriteStats {
	c := &writeStatsCache
	c.Lock()
	if c.path == benchmarkWriteStats && c.stats.TrackingSince != "" {
		stats := c.stats
		c.Unlock()
		return &stats
	}
	c.Unlock()

	data, err := os.ReadFile(benchmarkWriteStats)
	if err != nil {
		return nil
	}
	var stats WriteStats
	if err := json.Unmarshal(data, &stats); err != nil {
		return nil
	}
	return &stats
}

// ── TBW threshold helpers ──────────────────────────────────────────────────────

// DefaultTBWThresholdTB returns the capacity-aware default write endurance
// threshold: 500 × capacityGB / 1000 TB (e.g. 64 GB → 32 TB, 256 GB → 128 TB).
// Minimum returned value is 1.0 TB.
func DefaultTBWThresholdTB(capacityGB float64) float64 {
	if capacityGB <= 0 {
		return 32.0 // safe fallback when card size is unknown
	}
	t := 500 * capacityGB / 1000
	if t < 1.0 {
		t = 1.0
	}
	// Round to 1 decimal place.
	return math.Round(t*10) / 10
}

// readTBWThresholdTB reads the user-set threshold from disk, or returns the
// capacity-aware default when no custom value has been configured.
func readTBWThresholdTB(capacityGB float64) float64 {
	data, err := os.ReadFile(tbwThresholdFile)
	if err == nil {
		if v, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64); err == nil && v > 0 {
			return v
		}
	}
	return DefaultTBWThresholdTB(capacityGB)
}

// writeTBWThresholdTB persists the user threshold to disk.
func writeTBWThresholdTB(tb float64) error {
	return writeFileAtomic(tbwThresholdFile, []byte(strconv.FormatFloat(tb, 'f', 2, 64)), 0o644)
}

// isTBWWarning returns true when the total bytes written meets or exceeds the
// threshold expressed in terabytes.
func isTBWWarning(totalBytesWritten int64, thresholdTB float64) bool {
	if totalBytesWritten <= 0 || thresholdTB <= 0 {
		return false
	}
	return float64(totalBytesWritten)/1e12 >= thresholdTB
}

// ── HTTP handlers ──────────────────────────────────────────────────────────────

// HandleBenchmarkStatus returns the current benchmark state.
// GET /api/benchmark/status
func HandleBenchmarkStatus(w http.ResponseWriter, r *http.Request) {
	results := readBenchmarkResults()
	if results == nil {
		results = []BenchmarkResult{}
	}

	cardInfo := readSdCardInfo()
	capGB := 0.0
	if cardInfo != nil {
		capGB = cardInfo.CapacityGB
	}
	defaultTB := DefaultTBWThresholdTB(capGB)
	thresholdTB := readTBWThresholdTB(capGB)

	writeStats := readWriteStats()
	writeWarning := false
	if writeStats != nil {
		writeWarning = isTBWWarning(writeStats.TotalBytesWritten, thresholdTB)
	}

	status := BenchmarkStatus{
		Running:            isBenchmarkRunning(),
		Results:            results,
		CardInfo:           cardInfo,
		WriteStats:         writeStats,
		WriteWarning:       writeWarning,
		ThresholdTB:        thresholdTB,
		DefaultThresholdTB: defaultTB,
	}
	if len(results) > 0 {
		status.LatestRating = results[0].Rating
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// HandleBenchmarkThreshold reads or writes the TBW warning threshold.
// GET  /api/benchmark/threshold        — returns current threshold + default + capacity
// POST /api/benchmark/threshold        — body {"threshold_tb": 32.0} sets a custom value
func HandleBenchmarkThreshold(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	cardInfo := readSdCardInfo()
	capGB := 0.0
	if cardInfo != nil {
		capGB = cardInfo.CapacityGB
	}
	defaultTB := DefaultTBWThresholdTB(capGB)

	if r.Method == http.MethodPost {
		var req struct {
			ThresholdTB float64 `json:"threshold_tb"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		if req.ThresholdTB <= 0 {
			http.Error(w, `{"error":"threshold_tb must be positive"}`, http.StatusBadRequest)
			return
		}
		if err := writeTBWThresholdTB(req.ThresholdTB); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"write failed: %s"}`, err), http.StatusInternalServerError)
			return
		}
	}

	thresholdTB := readTBWThresholdTB(capGB)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"threshold_tb":         thresholdTB,
		"default_threshold_tb": defaultTB,
		"capacity_gb":          capGB,
	})
}

// HandleBenchmarkRun starts the benchmark and streams progress as SSE.
// POST /api/benchmark/run
func HandleBenchmarkRun(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	if isArchiving() {
		http.Error(w, `{"error":"archive is running — wait for it to finish before benchmarking"}`, http.StatusConflict)
		return
	}

	if !trySetBenchmarkRunning() {
		http.Error(w, `{"error":"benchmark already running"}`, http.StatusConflict)
		return
	}
	defer setBenchmarkRunning(false)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	emit := func(line string) {
		encoded, _ := json.Marshal(line)
		fmt.Fprintf(w, "data: %s\n\n", encoded)
		flusher.Flush()
	}
	emitJSON := func(v interface{}) {
		data, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	emit("Starting 160 MB write test (timeout 60s)…")

	// Resolve script path — fall back to run/benchmark.sh beside the binary.
	script := benchmarkScript
	if _, err := os.Stat(script); err != nil {
		// Dev mode: look relative to working directory.
		script = "../../run/benchmark.sh"
	}

	cmd := exec.CommandContext(r.Context(), "bash", script)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		emitJSON(map[string]string{"error": err.Error()})
		return
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		emitJSON(map[string]string{"error": err.Error()})
		return
	}

	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		// The last line of benchmark.sh stdout is the JSON result.
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			var result BenchmarkResult
			if json.Unmarshal([]byte(line), &result) == nil {
				emitJSON(map[string]interface{}{"result": result})
				continue
			}
		}
		emit(line)
	}

	if err := cmd.Wait(); err != nil {
		emitJSON(map[string]string{"error": fmt.Sprintf("benchmark failed: %v", err)})
		return
	}

	emitJSON(map[string]bool{"done": true})
}
