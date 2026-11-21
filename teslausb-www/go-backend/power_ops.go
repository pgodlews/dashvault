package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	powerConfigPath    = "/mutable/power_config.json"
	thermalHistoryPath = "/mutable/thermal_history.jsonl"
	powerBaselinePath  = "/mutable/power_baseline.json"
	powerSysRoot       = "" // empty in production; overridden in tests
)

const (
	maxDiskHistorySamples = 1440 // 24 hours at 1-minute resolution
	maxMemRecentSamples   = 240  // 1 hour at 15-second resolution
	memSampleInterval     = 15 * time.Second
	diskSampleInterval    = 60 * time.Second
)

// PowerConfig holds persistent settings for temperature logging, power profile
// selection, and dynamic thermal soft-throttling.
type PowerConfig struct {
	TempLoggingEnabled     bool    `json:"temp_logging_enabled"`
	PowerProfile           string  `json:"power_profile"` // "system" | "auto" | "powersave" | "conservative" | "schedutil" | "ondemand" | "interactive" | "performance"
	DynamicThrottleEnabled bool    `json:"dynamic_throttle_enabled"`
	WarnTempC              float64 `json:"warn_temp_c"`
	CritTempC              float64 `json:"crit_temp_c"`
	HysteresisC            float64 `json:"hysteresis_c"`
}

// PowerSummary is included in every /api/status snapshot.
type PowerSummary struct {
	CPUMHz                 float64  `json:"cpu_mhz"`
	GPUTempC               *float64 `json:"gpu_temp_c,omitempty"`
	Governor               string   `json:"governor,omitempty"`
	Profile                string   `json:"profile"`
	ThermalState           string   `json:"thermal_state"` // "normal" | "stage1_warn" | "stage2_crit"
	TempLoggingEnabled     bool     `json:"temp_logging_enabled"`
	DynamicThrottleEnabled bool     `json:"dynamic_throttle_enabled"`
}

// PowerCapabilities describes the hardware power-management features detected on the board.
type PowerCapabilities struct {
	AvailableGovernors      []string `json:"available_governors"`
	AvailableFrequenciesMHz []int    `json:"available_frequencies_mhz"`
	MinFreqMHz              int      `json:"min_freq_mhz"`
	MaxFreqMHz              int      `json:"max_freq_mhz"`
	MidFreqMHz              int      `json:"mid_freq_mhz"`
	HasDevfreq              bool     `json:"has_devfreq"`
	HasGPUTemp              bool     `json:"has_gpu_temp"`
}

// PowerConfigResponse is returned by GET/POST /api/power/config.
type PowerConfigResponse struct {
	Config       PowerConfig       `json:"config"`
	Capabilities PowerCapabilities `json:"capabilities"`
	Status       PowerSummary      `json:"status"`
}

// ThermalSample is a single point in the temperature & clock history log.
type ThermalSample struct {
	Timestamp    string   `json:"ts"`
	CPUTempC     float64  `json:"cpu_temp_c"`
	GPUTempC     *float64 `json:"gpu_temp_c,omitempty"`
	CPUMHz       float64  `json:"cpu_mhz"`
	Governor     string   `json:"governor,omitempty"`
	ThermalState string   `json:"thermal_state,omitempty"`
	ArchiveState string   `json:"archive_state,omitempty"`
}

// ThermalHistoryResponse is returned by GET /api/power/history.
type ThermalHistoryResponse struct {
	TempLoggingEnabled bool            `json:"temp_logging_enabled"`
	SampleCount        int             `json:"sample_count"`
	MinTempC           float64         `json:"min_temp_c"`
	MaxTempC           float64         `json:"max_temp_c"`
	AvgTempC           float64         `json:"avg_temp_c"`
	Samples            []ThermalSample `json:"samples"`
}

type powerRuntimeState struct {
	mu              sync.Mutex
	thermalState    string
	lastAppliedGov  string
	lastAppliedMax  int
	lastAppliedDev  string
	lastMemSample   time.Time
	lastDiskSample  time.Time
	memSamples      []ThermalSample
	modprobeAttempt bool
	diskLines       int // -1 = unknown (count on next append)
}

var powerState = &powerRuntimeState{
	thermalState: "normal",
	diskLines:    -1,
}

// powerBaseline records the board's original (system-managed) power settings the
// first time the backend takes control, so that switching back to "system"
// restores exactly what archiveloop / the OS configured instead of a guess.
type powerBaseline struct {
	Governor    string            `json:"governor"`
	MaxFreqKHz  int               `json:"max_freq_khz"`
	DevfreqGovs map[string]string `json:"devfreq_govs,omitempty"`
}

func defaultPowerConfig() PowerConfig {
	return PowerConfig{
		TempLoggingEnabled:     true,     // enabled by default
		PowerProfile:           "system", // disabled / unmanaged by default
		DynamicThrottleEnabled: false,    // disabled by default
		WarnTempC:              68.0,
		CritTempC:              75.0,
		HysteresisC:            4.0,
	}
}

func normalizePowerConfig(cfg PowerConfig) PowerConfig {
	validProfiles := map[string]bool{
		"system":       true,
		"auto":         true,
		"powersave":    true,
		"conservative": true,
		"schedutil":    true,
		"ondemand":     true,
		"interactive":  true,
		"performance":  true,
	}
	if !validProfiles[cfg.PowerProfile] {
		cfg.PowerProfile = "system"
	}
	if cfg.WarnTempC < 45 || cfg.WarnTempC > 95 || math.IsNaN(cfg.WarnTempC) {
		cfg.WarnTempC = 68.0
	}
	if cfg.CritTempC <= cfg.WarnTempC || cfg.CritTempC > 100 || math.IsNaN(cfg.CritTempC) {
		cfg.CritTempC = math.Max(cfg.WarnTempC+5.0, 75.0)
	}
	if cfg.HysteresisC < 1.0 || cfg.HysteresisC > 15.0 || math.IsNaN(cfg.HysteresisC) {
		cfg.HysteresisC = 4.0
	}
	return cfg
}

func readPowerConfig() PowerConfig {
	cfg := defaultPowerConfig()
	data, err := os.ReadFile(powerConfigPath)
	if err != nil {
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return defaultPowerConfig()
	}
	return normalizePowerConfig(cfg)
}

func writePowerConfig(cfg PowerConfig) error {
	cfg = normalizePowerConfig(cfg)
	if err := os.MkdirAll(filepath.Dir(powerConfigPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(powerConfigPath, append(data, '\n'), 0o644)
}

func sysPath(procRoot, rel string) string {
	root := procRoot
	if root == "" {
		root = powerSysRoot
	}
	return root + rel
}

func readCPUMHz(procRoot string) float64 {
	for _, name := range []string{"scaling_cur_freq", "cpuinfo_cur_freq"} {
		data, err := os.ReadFile(sysPath(procRoot, "/sys/devices/system/cpu/cpu0/cpufreq/"+name))
		if err != nil {
			continue
		}
		khz, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
		if err == nil && khz > 0 {
			return math.Round((khz/1000.0)*10) / 10
		}
	}
	return 0
}

func readCPUGovernor(procRoot string) string {
	data, err := os.ReadFile(sysPath(procRoot, "/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// readGPUTemp returns the temperature of a secondary GPU thermal zone when the
// SoC exposes one (e.g. RK3566 "gpu-thermal"). Boards with a single CPU zone
// (Raspberry Pi) return nil.
func readGPUTemp(procRoot string) *float64 {
	zones, _ := filepath.Glob(sysPath(procRoot, "/sys/class/thermal/thermal_zone*"))
	sort.Strings(zones)
	zone := ""
	for _, z := range zones {
		if t, err := os.ReadFile(filepath.Join(z, "type")); err == nil && strings.Contains(strings.ToLower(string(t)), "gpu") {
			zone = z
			break
		}
	}
	if zone == "" {
		// Fallback for kernels that don't expose a type file: treat zone1 as secondary.
		if _, err := os.Stat(sysPath(procRoot, "/sys/class/thermal/thermal_zone1/type")); err == nil {
			return nil
		}
		zone = sysPath(procRoot, "/sys/class/thermal/thermal_zone1")
	}
	data, err := os.ReadFile(filepath.Join(zone, "temp"))
	if err != nil {
		return nil
	}
	millideg, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil || millideg <= 0 {
		return nil
	}
	v := math.Round((millideg/1000.0)*10) / 10
	return &v
}

func detectPowerCapabilities(procRoot string) PowerCapabilities {
	caps := PowerCapabilities{
		AvailableGovernors:      []string{},
		AvailableFrequenciesMHz: []int{},
	}
	if data, err := os.ReadFile(sysPath(procRoot, "/sys/devices/system/cpu/cpu0/cpufreq/scaling_available_governors")); err == nil {
		for _, g := range strings.Fields(string(data)) {
			if g != "userspace" {
				caps.AvailableGovernors = append(caps.AvailableGovernors, g)
			}
		}
	}
	var khzList []int
	if data, err := os.ReadFile(sysPath(procRoot, "/sys/devices/system/cpu/cpu0/cpufreq/scaling_available_frequencies")); err == nil {
		seen := map[int]bool{}
		for _, f := range strings.Fields(string(data)) {
			if khz, err := strconv.Atoi(f); err == nil && khz > 0 {
				mhz := khz / 1000
				if !seen[mhz] {
					seen[mhz] = true
					caps.AvailableFrequenciesMHz = append(caps.AvailableFrequenciesMHz, mhz)
					khzList = append(khzList, khz)
				}
			}
		}
		sort.Ints(caps.AvailableFrequenciesMHz)
		sort.Ints(khzList)
	}
	if len(caps.AvailableFrequenciesMHz) > 0 {
		caps.MinFreqMHz = caps.AvailableFrequenciesMHz[0]
		caps.MaxFreqMHz = caps.AvailableFrequenciesMHz[len(caps.AvailableFrequenciesMHz)-1]
		caps.MidFreqMHz = caps.AvailableFrequenciesMHz[len(caps.AvailableFrequenciesMHz)/2]
	} else {
		if data, err := os.ReadFile(sysPath(procRoot, "/sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_min_freq")); err == nil {
			if khz, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && khz > 0 {
				caps.MinFreqMHz = khz / 1000
			}
		}
		if data, err := os.ReadFile(sysPath(procRoot, "/sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq")); err == nil {
			if khz, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && khz > 0 {
				caps.MaxFreqMHz = khz / 1000
			}
		}
		if caps.MinFreqMHz > 0 && caps.MaxFreqMHz > 0 {
			caps.MidFreqMHz = (caps.MinFreqMHz + caps.MaxFreqMHz) / 2
		}
	}
	if _, err := os.Stat(sysPath(procRoot, "/sys/class/devfreq/dmc/governor")); err == nil {
		caps.HasDevfreq = true
	}
	if readGPUTemp(procRoot) != nil {
		caps.HasGPUTemp = true
	}
	return caps
}

func computeThermalState(prev string, tempC float64, cfg PowerConfig) string {
	if !cfg.DynamicThrottleEnabled {
		return "normal"
	}
	switch prev {
	case "stage2_crit":
		if tempC <= cfg.WarnTempC-cfg.HysteresisC {
			return "normal"
		}
		if tempC <= cfg.CritTempC-cfg.HysteresisC {
			return "stage1_warn"
		}
		return "stage2_crit"
	case "stage1_warn":
		if tempC >= cfg.CritTempC {
			return "stage2_crit"
		}
		if tempC <= cfg.WarnTempC-cfg.HysteresisC {
			return "normal"
		}
		return "stage1_warn"
	default:
		if tempC >= cfg.CritTempC {
			return "stage2_crit"
		}
		if tempC >= cfg.WarnTempC {
			return "stage1_warn"
		}
		return "normal"
	}
}

// pickGovernor returns want if the kernel supports it, otherwise the closest
// available alternative. An empty available list means "unknown" -> try want.
func pickGovernor(want string, available []string) string {
	if len(available) == 0 {
		return want
	}
	has := func(g string) bool {
		for _, a := range available {
			if a == g {
				return true
			}
		}
		return false
	}
	if has(want) {
		return want
	}
	for _, alt := range []string{"conservative", "ondemand", "schedutil", "powersave"} {
		if has(alt) {
			return alt
		}
	}
	return available[0]
}

func devfreqGovernorPaths(procRoot string) []string {
	var out []string
	if p := sysPath(procRoot, "/sys/class/devfreq/dmc/governor"); fileExists(p) {
		out = append(out, p)
	}
	if matches, err := filepath.Glob(sysPath(procRoot, "/sys/class/devfreq/*.gpu/governor")); err == nil {
		out = append(out, matches...)
	}
	return out
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// capturePowerBaseline snapshots the current system-managed settings once,
// before the backend writes anything to sysfs. Existing baselines are kept so a
// backend restart while managed doesn't record the managed values as "original".
func capturePowerBaseline(procRoot string) {
	if fileExists(powerBaselinePath) {
		return
	}
	b := powerBaseline{Governor: readCPUGovernor(procRoot), DevfreqGovs: map[string]string{}}
	if data, err := os.ReadFile(sysPath(procRoot, "/sys/devices/system/cpu/cpu0/cpufreq/scaling_max_freq")); err == nil {
		b.MaxFreqKHz, _ = strconv.Atoi(strings.TrimSpace(string(data)))
	}
	root := sysPath(procRoot, "")
	for _, p := range devfreqGovernorPaths(procRoot) {
		if data, err := os.ReadFile(p); err == nil {
			b.DevfreqGovs[strings.TrimPrefix(p, root)] = strings.TrimSpace(string(data))
		}
	}
	if data, err := json.MarshalIndent(b, "", "  "); err == nil {
		_ = os.MkdirAll(filepath.Dir(powerBaselinePath), 0o755)
		_ = writeFileAtomic(powerBaselinePath, append(data, '\n'), 0o644)
	}
}

// restorePowerBaseline writes back the snapshot taken by capturePowerBaseline
// and deletes it. Without a snapshot, it falls back to conservative @ max clock.
func restorePowerBaseline(procRoot string, caps PowerCapabilities) {
	data, err := os.ReadFile(powerBaselinePath)
	var b powerBaseline
	if err != nil || json.Unmarshal(data, &b) != nil {
		applySysfsPower(procRoot, pickGovernor("conservative", caps.AvailableGovernors), caps.MaxFreqMHz, "default")
		return
	}
	root := sysPath(procRoot, "")
	if b.MaxFreqKHz > 0 {
		_ = os.WriteFile(root+"/sys/devices/system/cpu/cpu0/cpufreq/scaling_max_freq", []byte(strconv.Itoa(b.MaxFreqKHz)+"\n"), 0o644)
	}
	if b.Governor != "" {
		_ = os.WriteFile(root+"/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor", []byte(b.Governor+"\n"), 0o644)
	}
	for rel, gov := range b.DevfreqGovs {
		if strings.HasPrefix(rel, "/sys/class/devfreq/") && !strings.Contains(rel, "..") {
			_ = os.WriteFile(root+rel, []byte(gov+"\n"), 0o644)
		}
	}
	_ = os.Remove(powerBaselinePath)
}

func applySysfsPower(procRoot, gov string, maxFreqMHz int, devGov string) {
	root := procRoot
	if root == "" {
		root = powerSysRoot
	}
	if root == "" && !powerState.modprobeAttempt {
		powerState.modprobeAttempt = true
		_ = exec.Command("modprobe", "cpufreq_powersave").Run()
		_ = exec.Command("modprobe", "governor_powersave").Run()
		_ = exec.Command("modprobe", "governor_performance").Run()
	}
	if maxFreqMHz > 0 {
		khzStr := strconv.Itoa(maxFreqMHz * 1000)
		_ = os.WriteFile(root+"/sys/devices/system/cpu/cpu0/cpufreq/scaling_max_freq", []byte(khzStr+"\n"), 0o644)
	}
	if gov != "" {
		_ = os.WriteFile(root+"/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor", []byte(gov+"\n"), 0o644)
	}
	if devGov != "" {
		dmcGov := devGov
		gpuGov := devGov
		if devGov == "default" {
			dmcGov = "dmc_ondemand"
			gpuGov = "simple_ondemand"
		}
		if _, err := os.Stat(root + "/sys/class/devfreq/dmc/governor"); err == nil {
			_ = os.WriteFile(root+"/sys/class/devfreq/dmc/governor", []byte(dmcGov+"\n"), 0o644)
		}
		if matches, err := filepath.Glob(root + "/sys/class/devfreq/*.gpu/governor"); err == nil {
			for _, p := range matches {
				_ = os.WriteFile(p, []byte(gpuGov+"\n"), 0o644)
			}
		}
	}
}

func (c *Collector) collectPowerAndThermal(cpuTempC float64, archiveState string) (*PowerSummary, *float64) {
	cfg := readPowerConfig()
	gpuTemp := readGPUTemp(c.ProcRoot)
	caps := detectPowerCapabilities(c.ProcRoot)

	effectiveTemp := cpuTempC
	if gpuTemp != nil && *gpuTemp > effectiveTemp {
		effectiveTemp = *gpuTemp
	}

	powerState.mu.Lock()
	defer powerState.mu.Unlock()

	newThermalState := computeThermalState(powerState.thermalState, effectiveTemp, cfg)
	powerState.thermalState = newThermalState

	// Reconcile hardware power state ONLY when either a non-system profile is selected
	// or dynamic thermal soft-throttling is enabled.
	if cfg.PowerProfile != "system" || cfg.DynamicThrottleEnabled {
		targetGov := "conservative"
		targetMaxMHz := caps.MaxFreqMHz
		targetDev := "default"

		switch cfg.PowerProfile {
		case "auto":
			if archiveState == "archiving" {
				targetGov = "conservative"
				targetDev = "default"
			} else {
				targetGov = "powersave"
				targetDev = "powersave"
			}
		case "powersave":
			targetGov = "powersave"
			targetDev = "powersave"
		case "performance":
			targetGov = "performance"
			targetDev = "performance"
		case "conservative", "schedutil", "ondemand", "interactive":
			targetGov = cfg.PowerProfile
			targetDev = "default"
		default:
			targetGov = "conservative"
			targetDev = "default"
		}

		targetGov = pickGovernor(targetGov, caps.AvailableGovernors)

		// Apply dynamic thermal soft-throttling overrides if active
		switch newThermalState {
		case "stage2_crit":
			targetGov = pickGovernor("powersave", caps.AvailableGovernors)
			if caps.MinFreqMHz > 0 {
				targetMaxMHz = caps.MinFreqMHz
			}
			targetDev = "powersave"
		case "stage1_warn":
			if targetGov == "performance" {
				targetGov = pickGovernor("conservative", caps.AvailableGovernors)
			}
			if caps.MidFreqMHz > 0 {
				targetMaxMHz = caps.MidFreqMHz
			}
			targetDev = "powersave"
		}

		if targetGov != powerState.lastAppliedGov || targetMaxMHz != powerState.lastAppliedMax || targetDev != powerState.lastAppliedDev {
			capturePowerBaseline(c.ProcRoot)
			applySysfsPower(c.ProcRoot, targetGov, targetMaxMHz, targetDev)
			powerState.lastAppliedGov = targetGov
			powerState.lastAppliedMax = targetMaxMHz
			powerState.lastAppliedDev = targetDev
		}
	}

	cpuMHz := readCPUMHz(c.ProcRoot)
	gov := readCPUGovernor(c.ProcRoot)

	now := time.Now().UTC()
	if cfg.TempLoggingEnabled && cpuTempC > 0 {
		sample := ThermalSample{
			Timestamp:    now.Format(time.RFC3339),
			CPUTempC:     math.Round(cpuTempC*10) / 10,
			GPUTempC:     gpuTemp,
			CPUMHz:       cpuMHz,
			Governor:     gov,
			ThermalState: newThermalState,
			ArchiveState: archiveState,
		}
		if powerState.lastMemSample.IsZero() || now.Sub(powerState.lastMemSample) >= memSampleInterval {
			powerState.lastMemSample = now
			powerState.memSamples = append(powerState.memSamples, sample)
			if len(powerState.memSamples) > maxMemRecentSamples {
				powerState.memSamples = powerState.memSamples[len(powerState.memSamples)-maxMemRecentSamples:]
			}
		}
		if c.ProcRoot == "" && (powerState.lastDiskSample.IsZero() || now.Sub(powerState.lastDiskSample) >= diskSampleInterval) {
			powerState.lastDiskSample = now
			appendDiskThermalSample(sample)
		}
	}

	return &PowerSummary{
		CPUMHz:                 cpuMHz,
		GPUTempC:               gpuTemp,
		Governor:               gov,
		Profile:                cfg.PowerProfile,
		ThermalState:           newThermalState,
		TempLoggingEnabled:     cfg.TempLoggingEnabled,
		DynamicThrottleEnabled: cfg.DynamicThrottleEnabled,
	}, gpuTemp
}

// appendDiskThermalSample appends one JSON line (~150 bytes) to the on-disk
// history. To keep SD-card wear negligible the file is only rewritten
// (compacted to the newest maxDiskHistorySamples lines) once it grows past
// 2x the cap, i.e. roughly once per day at 1-minute resolution.
// Caller must hold powerState.mu.
func appendDiskThermalSample(sample ThermalSample) {
	if err := os.MkdirAll(filepath.Dir(thermalHistoryPath), 0o755); err != nil {
		return
	}
	if powerState.diskLines < 0 {
		powerState.diskLines = len(readDiskThermalSamples())
	}
	b, err := json.Marshal(sample)
	if err != nil {
		return
	}
	f, err := os.OpenFile(thermalHistoryPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	_, werr := f.Write(append(b, '\n'))
	_ = f.Close()
	if werr != nil {
		return
	}
	powerState.diskLines++
	if powerState.diskLines <= 2*maxDiskHistorySamples {
		return
	}
	samples := readDiskThermalSamples()
	if len(samples) > maxDiskHistorySamples {
		samples = samples[len(samples)-maxDiskHistorySamples:]
	}
	var sb strings.Builder
	for _, s := range samples {
		if line, err := json.Marshal(s); err == nil {
			sb.Write(line)
			sb.WriteByte('\n')
		}
	}
	if writeFileAtomic(thermalHistoryPath, []byte(sb.String()), 0o644) == nil {
		powerState.diskLines = len(samples)
	}
}

func readDiskThermalSamples() []ThermalSample {
	f, err := os.Open(thermalHistoryPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []ThermalSample
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var s ThermalSample
		if err := json.Unmarshal([]byte(line), &s); err == nil && s.Timestamp != "" {
			out = append(out, s)
		}
	}
	return out
}

func buildThermalHistoryResponse() ThermalHistoryResponse {
	cfg := readPowerConfig()
	diskSamples := readDiskThermalSamples()

	powerState.mu.Lock()
	memSamples := append([]ThermalSample(nil), powerState.memSamples...)
	powerState.mu.Unlock()

	seen := make(map[string]bool, len(diskSamples)+len(memSamples))
	merged := make([]ThermalSample, 0, len(diskSamples)+len(memSamples))
	for _, s := range diskSamples {
		if !seen[s.Timestamp] {
			seen[s.Timestamp] = true
			merged = append(merged, s)
		}
	}
	for _, s := range memSamples {
		if !seen[s.Timestamp] {
			seen[s.Timestamp] = true
			merged = append(merged, s)
		}
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Timestamp < merged[j].Timestamp
	})
	if cutoff := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339); len(merged) > 0 {
		i := sort.Search(len(merged), func(i int) bool { return merged[i].Timestamp >= cutoff })
		merged = merged[i:]
	}

	resp := ThermalHistoryResponse{
		TempLoggingEnabled: cfg.TempLoggingEnabled,
		SampleCount:        len(merged),
		Samples:            merged,
	}
	if len(merged) > 0 {
		minT := merged[0].CPUTempC
		maxT := merged[0].CPUTempC
		sumT := 0.0
		for _, s := range merged {
			if s.CPUTempC < minT {
				minT = s.CPUTempC
			}
			if s.CPUTempC > maxT {
				maxT = s.CPUTempC
			}
			sumT += s.CPUTempC
		}
		resp.MinTempC = math.Round(minT*10) / 10
		resp.MaxTempC = math.Round(maxT*10) / 10
		resp.AvgTempC = math.Round((sumT/float64(len(merged)))*10) / 10
	}
	return resp
}

// HandlePowerConfig handles GET /api/power/config and POST /api/power/config.
func HandlePowerConfig(w http.ResponseWriter, r *http.Request) {
	caps := detectPowerCapabilities("")
	if r.Method == http.MethodGet {
		cfg := readPowerConfig()
		powerState.mu.Lock()
		thState := powerState.thermalState
		powerState.mu.Unlock()
		resp := PowerConfigResponse{
			Config:       cfg,
			Capabilities: caps,
			Status: PowerSummary{
				CPUMHz:                 readCPUMHz(""),
				GPUTempC:               readGPUTemp(""),
				Governor:               readCPUGovernor(""),
				Profile:                cfg.PowerProfile,
				ThermalState:           thState,
				TempLoggingEnabled:     cfg.TempLoggingEnabled,
				DynamicThrottleEnabled: cfg.DynamicThrottleEnabled,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	var incoming PowerConfig
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	prevCfg := readPowerConfig()
	cfg := normalizePowerConfig(incoming)
	if err := writePowerConfig(cfg); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	// If switching back to "system" with dynamic throttling off, restore default conservative + max freq once.
	powerState.mu.Lock()
	powerState.lastAppliedGov = ""
	powerState.lastAppliedMax = 0
	powerState.lastAppliedDev = ""
	if cfg.PowerProfile == "system" && !cfg.DynamicThrottleEnabled && (prevCfg.PowerProfile != "system" || prevCfg.DynamicThrottleEnabled) {
		restorePowerBaseline("", caps)
		powerState.thermalState = "normal"
	}
	powerState.mu.Unlock()

	// Run one immediate collection/reconciliation pass so the response reflects the applied profile.
	col := NewCollector()
	st := col.Collect()
	var summary PowerSummary
	if st.Power != nil {
		summary = *st.Power
	}
	resp := PowerConfigResponse{
		Config:       cfg,
		Capabilities: caps,
		Status:       summary,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandlePowerHistory handles GET /api/power/history.
func HandlePowerHistory(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(buildThermalHistoryResponse())
}

// HandlePowerHistoryClear handles POST /api/power/history/clear.
func HandlePowerHistoryClear(w http.ResponseWriter, _ *http.Request) {
	powerState.mu.Lock()
	powerState.memSamples = nil
	powerState.lastMemSample = time.Time{}
	powerState.lastDiskSample = time.Time{}
	powerState.diskLines = 0
	_ = os.Remove(thermalHistoryPath)
	powerState.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"cleared": true})
}
