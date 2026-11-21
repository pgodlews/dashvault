package main

import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Status is the full system snapshot returned by /api/status.
type Status struct {
	CollectedAt time.Time      `json:"collected_at"`
	UptimeSecs  float64        `json:"uptime_seconds"`
	CPUTempC    float64        `json:"cpu_temp_c"`
	GPUTempC    *float64       `json:"gpu_temp_c,omitempty"`
	FanRPM      *int           `json:"fan_rpm,omitempty"`
	External5V  *float64       `json:"external_5v,omitempty"`
	RTCBatteryV *float64       `json:"rtc_battery_v,omitempty"`
	Throttled   *uint32        `json:"throttled,omitempty"`
	Disks       []DiskStat     `json:"disks"`
	WiFi        *WiFiInfo      `json:"wifi,omitempty"`
	Archive     ArchiveStat    `json:"archive"`
	SdCard      *SdCardSummary `json:"sdcard,omitempty"`
	Power       *PowerSummary  `json:"power,omitempty"`
}

// SdCardSummary is the lightweight sdcard field included in every status tick.
type SdCardSummary struct {
	LatestRating     string   `json:"latest_rating,omitempty"` // "good"|"marginal"|"slow"|""
	LatestWriteMBps  *float64 `json:"latest_write_mbps,omitempty"`
	TotalWrittenGB   float64  `json:"total_written_gb,omitempty"`
	BenchmarkRunning bool     `json:"benchmark_running"`
	WriteWarning     bool     `json:"write_warning"` // true when TBW threshold reached
}

type DiskStat struct {
	Mount   string  `json:"mount"`
	TotalGB float64 `json:"total_gb"`
	UsedGB  float64 `json:"used_gb"`
	FreeGB  float64 `json:"free_gb"`
	UsedPct float64 `json:"used_pct"`
}

type WiFiInfo struct {
	SSID      string `json:"ssid"`
	LinkLevel int    `json:"link_level"`
	SignalDBm int    `json:"signal_dbm"`
	NoiseDBm  int    `json:"noise_dbm"`
}

type ArchiveStat struct {
	State            string `json:"state"`                       // "idle" | "archiving" | "error" | "unknown"
	Message          string `json:"message,omitempty"`           // human-readable detail from archive_state
	FilesTransferred int    `json:"files_transferred,omitempty"` // count of files rsync has sent so far
	CurrentFile      string `json:"current_file,omitempty"`      // last file rsync reported transferring
}

// Collector gathers system metrics. ProcRoot and ArchiveFile can be overridden
// in tests to point at a tmpdir with fake /proc entries.
type Collector struct {
	ProcRoot    string   // default "" (real /proc, /sys)
	DiskMounts  []string // mounts to stat
	ArchiveFile string   // teslausb archive state file
	// Vcgencmd can be injected for fixture-based tests.
	Vcgencmd func(...string) ([]byte, error)
}

func NewCollector() *Collector {
	return &Collector{
		ProcRoot:    "",
		DiskMounts:  []string{"/", "/mutable", "/backingfiles"},
		ArchiveFile: "/run/teslausb/archive_state",
	}
}

func (c *Collector) Collect() Status {
	// Update write-tracking counters on every collection tick.
	UpdateWriteStats()

	cpuTemp := c.readCPUTemp()
	archive := c.readArchive()
	power, gpuTemp := c.collectPowerAndThermal(cpuTemp, archive.State)

	return Status{
		CollectedAt: time.Now().UTC(),
		UptimeSecs:  c.readUptime(),
		CPUTempC:    cpuTemp,
		GPUTempC:    gpuTemp,
		FanRPM:      c.readFanRPM(),
		External5V:  c.readVoltage("EXT5V_V"),
		RTCBatteryV: c.readVoltage("BATT_V"),
		Throttled:   c.readThrottled(),
		Disks:       c.readDisks(),
		WiFi:        c.readWiFi(),
		Archive:     archive,
		SdCard:      c.readSdCardSummary(),
		Power:       power,
	}
}

func (c *Collector) readSdCardSummary() *SdCardSummary {
	results := readBenchmarkResults()
	stats := readWriteStats()

	summary := &SdCardSummary{
		BenchmarkRunning: isBenchmarkRunning(),
	}
	if len(results) > 0 {
		summary.LatestRating = results[0].Rating
		summary.LatestWriteMBps = results[0].WriteMBps
	}
	if stats != nil && stats.TotalBytesWritten > 0 {
		summary.TotalWrittenGB = float64(stats.TotalBytesWritten) / 1e9

		// Check TBW warning threshold (capacity-aware default or user override).
		cardInfo := readSdCardInfoSysfs()
		capGB := 0.0
		if cardInfo != nil {
			capGB = cardInfo.CapacityGB
		}
		summary.WriteWarning = isTBWWarning(stats.TotalBytesWritten, readTBWThresholdTB(capGB))
	}
	return summary
}

func (c *Collector) readUptime() float64 {
	data, err := os.ReadFile(c.ProcRoot + "/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(fields[0], 64)
	return v
}

func (c *Collector) readCPUTemp() float64 {
	data, err := os.ReadFile(c.ProcRoot + "/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0
	}
	millideg, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return 0
	}
	return millideg / 1000
}

func (c *Collector) readDisks() []DiskStat {
	var out []DiskStat
	for _, mount := range c.DiskMounts {
		var st syscall.Statfs_t
		if err := syscall.Statfs(mount, &st); err != nil {
			continue
		}
		bs := uint64(st.Bsize)
		total := float64(st.Blocks*bs) / 1e9
		free := float64(st.Bavail*bs) / 1e9
		used := float64((st.Blocks-st.Bfree)*bs) / 1e9
		pct := 0.0
		if st.Blocks > 0 {
			pct = float64(st.Blocks-st.Bfree) / float64(st.Blocks) * 100
		}
		out = append(out, DiskStat{
			Mount:   mount,
			TotalGB: total,
			UsedGB:  used,
			FreeGB:  free,
			UsedPct: pct,
		})
	}
	return out
}

// readWiFi parses /proc/net/wireless for signal/noise, then shells out to
// iwgetid for the SSID (no /proc equivalent exists for SSID).
func (c *Collector) readWiFi() *WiFiInfo {
	f, err := os.Open(c.ProcRoot + "/proc/net/wireless")
	if err != nil {
		return nil
	}
	defer f.Close()

	// /proc/net/wireless layout (after two header lines):
	//   wlan0: 0000  60.  -49.  -95.  0.  0.  0.  0.  0.  0.
	var iface string
	var link, signal, noise int

	sc := bufio.NewScanner(f)
	for lineNo := 0; sc.Scan(); lineNo++ {
		if lineNo < 2 {
			continue
		}
		parts := strings.Fields(sc.Text())
		if len(parts) < 5 {
			continue
		}
		iface = strings.TrimSuffix(parts[0], ":")
		link, _ = strconv.Atoi(strings.TrimSuffix(parts[2], "."))
		signal, _ = strconv.Atoi(strings.TrimSuffix(parts[3], "."))
		noise, _ = strconv.Atoi(strings.TrimSuffix(parts[4], "."))
		break
	}
	if iface == "" {
		return nil
	}

	ssid := ""
	if out, err := exec.Command("iwgetid", iface, "-r").Output(); err == nil {
		ssid = strings.TrimSpace(string(out))
	}

	return &WiFiInfo{
		SSID:      ssid,
		LinkLevel: link,
		SignalDBm: signal,
		NoiseDBm:  noise,
	}
}

// readArchive reads the state file that archiveloop writes.
// Expected content: "idle", "archiving: <msg>", or "error: <msg>".
// When archiving, it also parses the rsync log for live file-transfer progress.
func (c *Collector) readArchive() ArchiveStat {
	data, err := os.ReadFile(c.ArchiveFile)
	if err != nil {
		return ArchiveStat{State: "unknown"}
	}
	line := strings.TrimSpace(string(data))
	for _, prefix := range []string{"archiving", "error"} {
		if strings.HasPrefix(line, prefix) {
			msg := ""
			if parts := strings.SplitN(line, ":", 2); len(parts) == 2 {
				msg = strings.TrimSpace(parts[1])
			}
			stat := ArchiveStat{State: prefix, Message: msg}
			if prefix == "archiving" {
				stat.FilesTransferred, stat.CurrentFile = readRsyncProgress()
			}
			return stat
		}
	}
	return ArchiveStat{State: "idle"}
}

// readRsyncProgress parses /tmp/archive-rsync-cmd.log (written by rsync
// --log-file in archive-clips.sh) and returns the count of files transferred
// so far and the name of the most recently transferred file.
//
// rsync log lines look like:
//
//	2024/01/15 10:23:45 [12345] >f+++++++++ SentryClips/2024-01-14_18-00-00/front.mp4
//
// The ">f" itemize flag means a regular file was sent.
func readRsyncProgress() (transferred int, currentFile string) {
	data, err := os.ReadFile("/tmp/archive-rsync-cmd.log")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		// Find the itemize-changes token ">f…" that rsync writes per file.
		idx := strings.Index(line, "] >f")
		if idx < 0 {
			continue
		}
		transferred++
		// Everything after "] >f<flags> " is the relative path.
		rest := line[idx+4:]
		if sp := strings.Index(rest, " "); sp >= 0 {
			currentFile = strings.TrimSpace(rest[sp+1:])
		}
	}
	return
}
