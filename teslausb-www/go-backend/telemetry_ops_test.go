package main

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func redirectTelemetryPaths(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	originalStore, originalClips, originalConf := telemetryStoreDir, teslaCamRoot, confFilePath
	telemetryStoreDir = filepath.Join(root, "store")
	teslaCamRoot = filepath.Join(root, "TeslaCam")
	confFilePath = filepath.Join(root, "teslausb_setup_variables.conf")
	t.Cleanup(func() {
		telemetryStoreDir, teslaCamRoot, confFilePath = originalStore, originalClips, originalConf
	})
}

func writeTelemetryFixture(t *testing.T, state string) TelemetryClip {
	t.Helper()
	clip := TelemetryClip{
		ID: "SavedClips/2026-11-07_10-00-00", RecordedAt: "2026-11-07T10:00:00Z",
		State: state, FrontFile: "2026-11-07_10-00-00-front.mp4", Duration: 1.1, Samples: 2,
	}
	data, err := json.Marshal(map[string]any{"version": 1, "clips": []TelemetryClip{clip}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(telemetryStoreDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(telemetryStoreDir, "clips.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	track, err := telemetryTrackPath(clip.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(track), 0o700); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 32)
	for i, value := range []float32{0, 53.1, -9.5, 8, 1.1, 53.1001, -9.5001, 8.1} {
		binary.LittleEndian.PutUint32(buffer[i*4:], math.Float32bits(value))
	}
	if err := os.WriteFile(track, buffer, 0o600); err != nil {
		t.Fatal(err)
	}
	return clip
}

func TestLoadTelemetryClipsDecodesTracksAndAvailability(t *testing.T) {
	redirectTelemetryPaths(t)
	clip := writeTelemetryFixture(t, "available")
	clipDir := filepath.Join(teslaCamRoot, clip.ID)
	if err := os.MkdirAll(clipDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clipDir, clip.FrontFile), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	clips, err := loadTelemetryClips()
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 1 || clips[0].State != "available" || len(clips[0].Track) != 2 {
		t.Fatalf("unexpected clips: %+v", clips)
	}
	if math.Abs(clips[0].Track[1].Seconds-1.1) > 1e-6 {
		t.Errorf("track timestamp = %f", clips[0].Track[1].Seconds)
	}
	if err := os.Remove(filepath.Join(clipDir, clip.FrontFile)); err != nil {
		t.Fatal(err)
	}
	clips, err = loadTelemetryClips()
	if err != nil {
		t.Fatal(err)
	}
	if clips[0].State != "overwritten" {
		t.Errorf("state = %s", clips[0].State)
	}
}

func TestTelemetryTrackPathSupportsMinuteAndLegacySavedIDs(t *testing.T) {
	for _, id := range []string{
		"SavedClips/2026-11-07_10-00-00",
		"SavedClips/2026-11-07_10-00-00/2026-11-07_10-00-00-front.mp4",
		"RecentClips/2026-11-07/2026-11-07_10-00-00-front.mp4",
	} {
		if _, err := telemetryTrackPath(id); err != nil {
			t.Errorf("valid clip ID %q rejected: %v", id, err)
		}
	}
}

func TestTelemetrySettingsDefaultAndWrite(t *testing.T) {
	redirectTelemetryPaths(t)
	if got := readTelemetrySettings(); got.Enabled || got.RetentionDays != 30 {
		t.Fatalf("unexpected defaults: %+v", got)
	}
	stubRemount(t)
	settings := TelemetrySettings{Enabled: true, RetentionDays: 14, TileURL: "https://tiles.example.local/{z}/{x}/{y}.png"}
	if err := writeTelemetrySettings(settings); err != nil {
		t.Fatal(err)
	}
	got := readTelemetrySettings()
	if got.WhileDriving == nil || *got.WhileDriving {
		t.Fatalf("while_driving should read as false when never set: %+v", got)
	}
	got.WhileDriving = nil
	if got != settings {
		t.Fatalf("settings = %+v, want %+v", got, settings)
	}
}

func TestTelemetryWhileDrivingIsKeptWhenOmitted(t *testing.T) {
	redirectTelemetryPaths(t)
	stubRemount(t)
	original := runTelemetryPrune
	t.Cleanup(func() { runTelemetryPrune = original })
	runTelemetryPrune = func(int) error { return nil }
	post := func(body string) TelemetrySettings {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/telemetry/settings", strings.NewReader(body))
		response := httptest.NewRecorder()
		HandleTelemetrySettings(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var got TelemetrySettings
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	base := `"enabled":true,"retention_days":30,"tile_url":"https://tiles.example.local/{z}/{x}/{y}.png"`
	if got := post(`{` + base + `,"while_driving":true}`); got.WhileDriving == nil || !*got.WhileDriving {
		t.Fatalf("while_driving not saved: %+v", got)
	}
	// An older UI that does not know the field must not switch it off.
	if got := post(`{` + base + `}`); got.WhileDriving == nil || !*got.WhileDriving {
		t.Fatalf("while_driving lost when omitted: %+v", got)
	}
	data, err := os.ReadFile(confFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "export TELEMETRY_WHILE_DRIVING='true'") {
		t.Fatalf("conf file: %s", data)
	}
	if got := post(`{` + base + `,"while_driving":false}`); got.WhileDriving == nil || *got.WhileDriving {
		t.Fatalf("while_driving not switched off: %+v", got)
	}
}

func TestReadTelemetryTrackExtendedFormat(t *testing.T) {
	redirectTelemetryPaths(t)
	clip := writeTelemetryFixture(t, "available")
	path, err := telemetryTrackPath(clip.ID)
	if err != nil {
		t.Fatal(err)
	}
	values := []float32{1, 53.1, -9.5, 12, 270, 1, 2, 0.1, 0.2, 9.8, 65, 1, -45, 1, 0}
	buffer := make([]byte, len(values)*4)
	for i, value := range values {
		binary.LittleEndian.PutUint32(buffer[i*4:], math.Float32bits(value))
	}
	if err := os.WriteFile(path, buffer, 0o600); err != nil {
		t.Fatal(err)
	}
	points, err := readTelemetryTrack(clip.ID, 15)
	if err != nil {
		t.Fatal(err)
	}
	point := points[0]
	if point.Gear != 1 || point.Autopilot != 2 || point.Accelerator != 65 || !point.Brake || !point.BlinkerLeft || point.BlinkerRight {
		t.Errorf("unexpected extended point: %+v", point)
	}

	indexPath := filepath.Join(telemetryStoreDir, "clips.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Clips []TelemetryClip `json:"clips"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	stored.Clips[0].PointWords = 15
	data, err = json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	clips, err := loadTelemetryClips()
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 1 || !clips[0].Extended || len(clips[0].Track) != 1 {
		t.Errorf("extended clip did not load: %+v", clips)
	}
}

func TestReadTelemetryTrackSkipsNonFiniteSamples(t *testing.T) {
	redirectTelemetryPaths(t)
	clip := writeTelemetryFixture(t, "available")
	path, err := telemetryTrackPath(clip.ID)
	if err != nil {
		t.Fatal(err)
	}
	values := []float32{0, 53.1, -9.5, 8, 1, 53.2, -9.5, float32(math.NaN())}
	buffer := make([]byte, len(values)*4)
	for i, value := range values {
		binary.LittleEndian.PutUint32(buffer[i*4:], math.Float32bits(value))
	}
	if err := os.WriteFile(path, buffer, 0o600); err != nil {
		t.Fatal(err)
	}
	points, err := readTelemetryTrack(clip.ID, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].Speed != 8 {
		t.Fatalf("points = %+v", points)
	}
	if _, err := json.Marshal(points); err != nil {
		t.Fatal(err)
	}
}

func TestTelemetrySettingsPruneImmediately(t *testing.T) {
	redirectTelemetryPaths(t)
	stubRemount(t)
	original := runTelemetryPrune
	t.Cleanup(func() { runTelemetryPrune = original })
	called := 0
	runTelemetryPrune = func(days int) error {
		called = days
		return nil
	}
	body := strings.NewReader(`{"enabled":true,"retention_days":14,"tile_url":"https://tiles.example.local/{z}/{x}/{y}.png"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/telemetry/settings", body)
	response := httptest.NewRecorder()
	HandleTelemetrySettings(response, req)
	if response.Code != http.StatusOK || called != 14 {
		t.Fatalf("status=%d prune=%d body=%s", response.Code, called, response.Body.String())
	}
}

func TestHandleTelemetryClipMatchesSavedEventPrefixFallback(t *testing.T) {
	redirectTelemetryPaths(t)
	if err := os.WriteFile(confFilePath, []byte("export TELEMETRY_MAP_ENABLED=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	clip := TelemetryClip{
		ID:         "SavedClips/2026-11-07_10-00-00/2026-11-07_10-00-00-front.mp4",
		RecordedAt: "2026-11-07T10:00:00Z",
		State:      "available",
		FrontFile:  "2026-11-07_10-00-00-front.mp4",
		Duration:   1.1,
		Samples:    1,
	}
	data, err := json.Marshal(map[string]any{"version": 1, "clips": []TelemetryClip{clip}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(telemetryStoreDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(telemetryStoreDir, "clips.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	track, err := telemetryTrackPath(clip.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(track), 0o700); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 16)
	for i, value := range []float32{0, 53.1, -9.5, 8} {
		binary.LittleEndian.PutUint32(buffer[i*4:], math.Float32bits(value))
	}
	if err := os.WriteFile(track, buffer, 0o600); err != nil {
		t.Fatal(err)
	}
	clipDir := filepath.Join(teslaCamRoot, "SavedClips/2026-11-07_10-00-00")
	if err := os.MkdirAll(clipDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clipDir, clip.FrontFile), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/telemetry/clip?id=SavedClips/2026-11-07_10-00-00", nil)
	rec := httptest.NewRecorder()
	HandleTelemetryClip(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var got TelemetryClip
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != clip.ID {
		t.Errorf("got ID %q, want %q", got.ID, clip.ID)
	}
}

