package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupFakePowerSysfs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sys", "class", "thermal", "thermal_zone0", "temp"), "52400\n")
	writeFile(t, filepath.Join(dir, "sys", "class", "thermal", "thermal_zone1", "temp"), "54100\n")
	writeFile(t, filepath.Join(dir, "sys", "class", "thermal", "thermal_zone1", "type"), "gpu-thermal\n")
	writeFile(t, filepath.Join(dir, "sys", "devices", "system", "cpu", "cpu0", "cpufreq", "scaling_available_governors"), "powersave conservative schedutil ondemand performance\n")
	writeFile(t, filepath.Join(dir, "sys", "devices", "system", "cpu", "cpu0", "cpufreq", "scaling_available_frequencies"), "408000 816000 1104000 1416000 1800000\n")
	writeFile(t, filepath.Join(dir, "sys", "devices", "system", "cpu", "cpu0", "cpufreq", "scaling_cur_freq"), "816000\n")
	writeFile(t, filepath.Join(dir, "sys", "devices", "system", "cpu", "cpu0", "cpufreq", "scaling_max_freq"), "1800000\n")
	writeFile(t, filepath.Join(dir, "sys", "devices", "system", "cpu", "cpu0", "cpufreq", "scaling_governor"), "conservative\n")
	writeFile(t, filepath.Join(dir, "sys", "class", "devfreq", "dmc", "governor"), "dmc_ondemand\n")
	return dir
}

func resetPowerGlobals(t *testing.T, sysDir string) {
	t.Helper()
	tmp := t.TempDir()
	origCfgPath := powerConfigPath
	origHistPath := thermalHistoryPath
	origSysRoot := powerSysRoot
	origBaseline := powerBaselinePath

	powerConfigPath = filepath.Join(tmp, "power_config.json")
	thermalHistoryPath = filepath.Join(tmp, "thermal_history.jsonl")
	powerSysRoot = sysDir
	powerBaselinePath = filepath.Join(tmp, "power_baseline.json")

	powerState.mu.Lock()
	powerState.thermalState = "normal"
	powerState.lastAppliedGov = ""
	powerState.lastAppliedMax = 0
	powerState.lastAppliedDev = ""
	powerState.lastMemSample = time.Time{}
	powerState.lastDiskSample = time.Time{}
	powerState.memSamples = nil
	powerState.modprobeAttempt = true
	powerState.diskLines = -1
	powerState.mu.Unlock()

	t.Cleanup(func() {
		powerConfigPath = origCfgPath
		thermalHistoryPath = origHistPath
		powerSysRoot = origSysRoot
		powerBaselinePath = origBaseline
	})
}

func TestDefaultPowerConfigDoesNotTouchSysfsButLogsTemp(t *testing.T) {
	sysDir := setupFakePowerSysfs(t)
	resetPowerGlobals(t, sysDir)

	cfg := readPowerConfig()
	if !cfg.TempLoggingEnabled {
		t.Fatalf("expected TempLoggingEnabled=true by default")
	}
	if cfg.PowerProfile != "system" {
		t.Fatalf("expected PowerProfile='system' by default, got %q", cfg.PowerProfile)
	}
	if cfg.DynamicThrottleEnabled {
		t.Fatalf("expected DynamicThrottleEnabled=false by default")
	}

	// Set a custom value in scaling_governor to verify default mode does not overwrite it.
	govPath := filepath.Join(sysDir, "sys", "devices", "system", "cpu", "cpu0", "cpufreq", "scaling_governor")
	writeFile(t, govPath, "schedutil\n")

	c := &Collector{ProcRoot: sysDir}
	st := c.Collect()
	if st.Power == nil {
		t.Fatal("expected st.Power != nil")
	}
	if st.Power.Governor != "schedutil" {
		t.Errorf("expected unmanaged governor to remain 'schedutil', got %q", st.Power.Governor)
	}
	if st.Power.CPUMHz != 816.0 {
		t.Errorf("expected CPUMHz=816.0, got %v", st.Power.CPUMHz)
	}
	if st.GPUTempC == nil || *st.GPUTempC != 54.1 {
		t.Errorf("expected GPUTempC=54.1, got %v", st.GPUTempC)
	}

	hist := buildThermalHistoryResponse()
	if hist.SampleCount != 1 {
		t.Fatalf("expected 1 logged thermal sample, got %d", hist.SampleCount)
	}
	if hist.Samples[0].CPUTempC != 52.4 {
		t.Errorf("expected sample CPUTempC=52.4, got %v", hist.Samples[0].CPUTempC)
	}
}

func TestAutoProfileAndDynamicThermalThrottling(t *testing.T) {
	sysDir := setupFakePowerSysfs(t)
	resetPowerGlobals(t, sysDir)

	cfg := PowerConfig{
		TempLoggingEnabled:     true,
		PowerProfile:           "auto",
		DynamicThrottleEnabled: true,
		WarnTempC:              68.0,
		CritTempC:              75.0,
		HysteresisC:            4.0,
	}
	if err := writePowerConfig(cfg); err != nil {
		t.Fatalf("writePowerConfig: %v", err)
	}

	govPath := filepath.Join(sysDir, "sys", "devices", "system", "cpu", "cpu0", "cpufreq", "scaling_governor")
	maxPath := filepath.Join(sysDir, "sys", "devices", "system", "cpu", "cpu0", "cpufreq", "scaling_max_freq")
	dmcPath := filepath.Join(sysDir, "sys", "class", "devfreq", "dmc", "governor")

	c := &Collector{ProcRoot: sysDir}

	// 1. Normal temp (52.4°C), idle archive -> auto selects powersave
	c.collectPowerAndThermal(52.4, "idle")
	if got, _ := os.ReadFile(govPath); strings.TrimSpace(string(got)) != "powersave" {
		t.Errorf("auto idle: want powersave, got %q", string(got))
	}
	if got, _ := os.ReadFile(dmcPath); strings.TrimSpace(string(got)) != "powersave" {
		t.Errorf("auto idle dmc: want powersave, got %q", string(got))
	}

	// 2. Normal temp (55.0°C), archiving -> auto boosts to conservative
	c.collectPowerAndThermal(55.0, "archiving")
	if got, _ := os.ReadFile(govPath); strings.TrimSpace(string(got)) != "conservative" {
		t.Errorf("auto archiving: want conservative, got %q", string(got))
	}
	if got, _ := os.ReadFile(maxPath); strings.TrimSpace(string(got)) != "1800000" {
		t.Errorf("auto archiving max_freq: want 1800000, got %q", string(got))
	}

	// 3. Stage 1 warn temp (70.0°C) during archiving -> caps max_freq to mid (1104 MHz) & dmc powersave
	summary, _ := c.collectPowerAndThermal(70.0, "archiving")
	if summary.ThermalState != "stage1_warn" {
		t.Errorf("want stage1_warn, got %q", summary.ThermalState)
	}
	if got, _ := os.ReadFile(maxPath); strings.TrimSpace(string(got)) != "1104000" {
		t.Errorf("stage1_warn max_freq: want 1104000, got %q", string(got))
	}
	if got, _ := os.ReadFile(dmcPath); strings.TrimSpace(string(got)) != "powersave" {
		t.Errorf("stage1_warn dmc: want powersave, got %q", string(got))
	}

	// 4. Stage 2 crit temp (76.5°C) -> forces powersave + min_freq (408 MHz)
	summary, _ = c.collectPowerAndThermal(76.5, "archiving")
	if summary.ThermalState != "stage2_crit" {
		t.Errorf("want stage2_crit, got %q", summary.ThermalState)
	}
	if got, _ := os.ReadFile(govPath); strings.TrimSpace(string(got)) != "powersave" {
		t.Errorf("stage2_crit gov: want powersave, got %q", string(got))
	}
	if got, _ := os.ReadFile(maxPath); strings.TrimSpace(string(got)) != "408000" {
		t.Errorf("stage2_crit max_freq: want 408000, got %q", string(got))
	}

	// 5. Hysteresis: at 73.0°C (above 75-4=71°C), remains in stage2_crit
	summary, _ = c.collectPowerAndThermal(73.0, "archiving")
	if summary.ThermalState != "stage2_crit" {
		t.Errorf("hysteresis at 73C: want stage2_crit, got %q", summary.ThermalState)
	}

	// 6. Cool down to 62.0°C (below 68-4=64°C) -> returns to normal
	summary, _ = c.collectPowerAndThermal(62.0, "archiving")
	if summary.ThermalState != "normal" {
		t.Errorf("recovery at 62C: want normal, got %q", summary.ThermalState)
	}
}

func TestPowerConfigAndHistoryEndpoints(t *testing.T) {
	sysDir := setupFakePowerSysfs(t)
	resetPowerGlobals(t, sysDir)

	// GET /api/power/config
	req := httptest.NewRequest(http.MethodGet, "/api/power/config", nil)
	rec := httptest.NewRecorder()
	HandlePowerConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/power/config: status %d", rec.Code)
	}
	var resp PowerConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Config.TempLoggingEnabled || resp.Config.PowerProfile != "system" || resp.Config.DynamicThrottleEnabled {
		t.Errorf("unexpected default config: %+v", resp.Config)
	}

	// POST /api/power/config
	body, _ := json.Marshal(PowerConfig{
		TempLoggingEnabled:     true,
		PowerProfile:           "powersave",
		DynamicThrottleEnabled: true,
		WarnTempC:              66,
		CritTempC:              74,
		HysteresisC:            4,
	})
	postReq := httptest.NewRequest(http.MethodPost, "/api/power/config", bytes.NewReader(body))
	postRec := httptest.NewRecorder()
	HandlePowerConfig(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/power/config: status %d: %s", postRec.Code, postRec.Body.String())
	}

	// Clear history
	clearReq := httptest.NewRequest(http.MethodPost, "/api/power/history/clear", nil)
	clearRec := httptest.NewRecorder()
	HandlePowerHistoryClear(clearRec, clearReq)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("POST /api/power/history/clear: status %d", clearRec.Code)
	}
}

func TestSwitchBackToSystemRestoresBaseline(t *testing.T) {
	sysDir := setupFakePowerSysfs(t)
	resetPowerGlobals(t, sysDir)

	govPath := filepath.Join(sysDir, "sys", "devices", "system", "cpu", "cpu0", "cpufreq", "scaling_governor")
	maxPath := filepath.Join(sysDir, "sys", "devices", "system", "cpu", "cpu0", "cpufreq", "scaling_max_freq")
	dmcPath := filepath.Join(sysDir, "sys", "class", "devfreq", "dmc", "governor")
	// Pretend the OS configured schedutil (not the hardcoded fallback).
	writeFile(t, govPath, "schedutil\n")

	post := func(cfg PowerConfig) {
		t.Helper()
		body, _ := json.Marshal(cfg)
		rec := httptest.NewRecorder()
		HandlePowerConfig(rec, httptest.NewRequest(http.MethodPost, "/api/power/config", bytes.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("POST status %d: %s", rec.Code, rec.Body.String())
		}
	}

	managed := defaultPowerConfig()
	managed.PowerProfile = "powersave"
	post(managed)
	if got, _ := os.ReadFile(govPath); strings.TrimSpace(string(got)) != "powersave" {
		t.Fatalf("managed: want powersave, got %q", got)
	}
	if !fileExists(powerBaselinePath) {
		t.Fatal("expected baseline snapshot to be written before first sysfs change")
	}

	post(defaultPowerConfig())
	if got, _ := os.ReadFile(govPath); strings.TrimSpace(string(got)) != "schedutil" {
		t.Errorf("restore gov: want schedutil, got %q", got)
	}
	if got, _ := os.ReadFile(maxPath); strings.TrimSpace(string(got)) != "1800000" {
		t.Errorf("restore max: want 1800000, got %q", got)
	}
	if got, _ := os.ReadFile(dmcPath); strings.TrimSpace(string(got)) != "dmc_ondemand" {
		t.Errorf("restore dmc: want dmc_ondemand, got %q", got)
	}
	if fileExists(powerBaselinePath) {
		t.Error("expected baseline snapshot to be removed after restore")
	}
}

func TestDiskHistoryIsAppendOnlyAndCompacts(t *testing.T) {
	sysDir := setupFakePowerSysfs(t)
	resetPowerGlobals(t, sysDir)

	base := time.Now().UTC().Add(-2 * time.Hour)
	powerState.mu.Lock()
	for i := 0; i < 2*maxDiskHistorySamples+1; i++ {
		appendDiskThermalSample(ThermalSample{
			Timestamp: base.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
			CPUTempC:  50,
		})
	}
	powerState.mu.Unlock()

	if n := len(readDiskThermalSamples()); n != maxDiskHistorySamples {
		t.Fatalf("after compaction want %d lines, got %d", maxDiskHistorySamples, n)
	}
}

func TestPickGovernorFallback(t *testing.T) {
	if g := pickGovernor("conservative", []string{"ondemand", "powersave", "performance"}); g != "ondemand" {
		t.Errorf("want ondemand fallback, got %q", g)
	}
	if g := pickGovernor("powersave", nil); g != "powersave" {
		t.Errorf("unknown list should pass through, got %q", g)
	}
}
