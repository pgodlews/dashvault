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
	"syscall"
	"testing"
	"time"
)

// point15 builds one 15-float sample: t, lat, lon, speed, then zeros.
func point15(t, lat, lon float32) []float32 {
	values := make([]float32, telemetryPointFloats)
	values[0], values[1], values[2], values[3] = t, lat, lon, 10
	return values
}

func floatBytes(values ...[]float32) []byte {
	var out []byte
	for _, group := range values {
		for _, v := range group {
			out = binary.LittleEndian.AppendUint32(out, math.Float32bits(v))
		}
	}
	return out
}

func writeDayFile(t *testing.T, day string, clips []TelemetryClip) {
	t.Helper()
	if err := os.MkdirAll(telemetryDaysDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"version": 2, "day": day, "clips": clips})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(telemetryDaysDir(), day+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeRawTrack(t *testing.T, id string, points ...[]float32) {
	t.Helper()
	path, err := telemetryTrackPath(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, floatBytes(points...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fixtureStore writes a packed day (2026-11-10, two clips) and a raw day
// (2026-11-11, one clip), as telemetry_index.py would after compaction.
func fixtureStore(t *testing.T) {
	t.Helper()
	redirectTelemetryPaths(t)
	if err := os.WriteFile(confFilePath, []byte("export TELEMETRY_MAP_ENABLED=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	packName := "2026-11-10.1731230000000"
	first := []TelemetryClip{
		{ID: "RecentClips/2026-11-10/2026-11-10_08-00-00-front.mp4", RecordedAt: "2026-11-10T08:00:00",
			State: "available", Duration: 59, Samples: 2, PointWords: 15,
			Pack: &TelemetryPack{Name: packName, Offset: 0, Count: 2, LineOffset: 0, LineCount: 2}},
		{ID: "SavedClips/2026-11-10_09-31-00/2026-11-10_09-30-00-front.mp4", RecordedAt: "2026-11-10T09:30:00",
			State: "archived", Duration: 59, Samples: 2, PointWords: 15,
			Pack: &TelemetryPack{Name: packName, Offset: 2, Count: 2, LineOffset: 2, LineCount: 2}},
		{ID: "RecentClips/2026-11-10/2026-11-10_09-45-00-front.mp4", RecordedAt: "2026-11-10T09:45:00",
			State: "no-gps", PointWords: 15},
	}
	writeDayFile(t, "2026-11-10", first)
	pack := floatBytes(point15(0, 53.1, 18.0), point15(58, 53.2, 18.1), point15(0, 54.1, 19.0), point15(58, 54.2, 19.1))
	if err := os.WriteFile(filepath.Join(telemetryDaysDir(), packName+".f32"), pack, 0o600); err != nil {
		t.Fatal(err)
	}
	line := floatBytes([]float32{53.1, 18.0, 53.2, 18.1, 54.1, 19.0, 54.2, 19.1})
	if err := os.WriteFile(filepath.Join(telemetryDaysDir(), packName+".line"), line, 0o600); err != nil {
		t.Fatal(err)
	}
	second := TelemetryClip{ID: "RecentClips/2026-11-11/2026-11-11_17-00-00-front.mp4", RecordedAt: "2026-11-11T17:00:00",
		State: "available", Duration: 2, Samples: 3, PointWords: 15}
	writeDayFile(t, "2026-11-11", []TelemetryClip{second})
	writeRawTrack(t, second.ID, point15(0, 55.0, 20.0), point15(1, 55.00001, 20.0), point15(2, 55.1, 20.1))
}

func getJSON(t *testing.T, handler http.HandlerFunc, url string, out any) int {
	t.Helper()
	response := httptest.NewRecorder()
	handler(response, httptest.NewRequest(http.MethodGet, url, nil))
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), out); err != nil {
			t.Fatalf("%s: %v: %s", url, err, response.Body.String())
		}
	}
	return response.Code
}

func TestRangedTracksReturnLinesFromPackedAndRawDays(t *testing.T) {
	fixtureStore(t)
	var got struct {
		From, To string
		Clips    []TelemetryClip
	}
	if code := getJSON(t, HandleTelemetryTracks, "/api/telemetry/tracks?from=2026-11-10T09:00&to=2026-11-12", &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if got.From != "2026-11-10T09:00:00" || got.To != "2026-11-12T00:00:00" {
		t.Fatalf("bounds %q %q", got.From, got.To)
	}
	if len(got.Clips) != 2 {
		t.Fatalf("want the 09:30 Saved clip and the 11 Nov clip, got %+v", got.Clips)
	}
	saved, raw := got.Clips[0], got.Clips[1]
	if saved.Line[1] != [2]float64{float64(float32(54.2)), float64(float32(19.1))} || saved.Track != nil || saved.Pack != nil {
		t.Fatalf("packed clip = %+v", saved)
	}
	// The raw clip is simplified on the fly: the middle point is within 5 m.
	if len(raw.Line) != 2 || raw.Line[0][0] != float64(float32(55.0)) {
		t.Fatalf("raw clip line = %+v", raw.Line)
	}
	if code := getJSON(t, HandleTelemetryTracks, "/api/telemetry/tracks?from=yesterday", &got); code != http.StatusBadRequest {
		t.Fatalf("bad bound gave %d", code)
	}
}

func TestRangedTracksFullForTripReplay(t *testing.T) {
	fixtureStore(t)
	var got struct{ Clips []TelemetryClip }
	url := "/api/telemetry/tracks?from=2026-11-10T07:59&to=2026-11-10T10:00&full=1"
	if code := getJSON(t, HandleTelemetryTracks, url, &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(got.Clips) != 2 || len(got.Clips[0].Track) != 2 || got.Clips[0].Line != nil {
		t.Fatalf("clips = %+v", got.Clips)
	}
	// Full tracks only for short ranges, so a click cannot ask for months of points.
	for _, bad := range []string{
		"/api/telemetry/tracks?from=2026-11-01&to=2026-11-10&full=1",
		"/api/telemetry/tracks?from=2026-11-10&full=1",
	} {
		if code := getJSON(t, HandleTelemetryTracks, bad, &got); code != http.StatusBadRequest {
			t.Fatalf("%s gave %d", bad, code)
		}
	}
}

func TestUnrangedTracksStillReturnFullTracks(t *testing.T) {
	fixtureStore(t)
	var got struct{ Clips []TelemetryClip }
	getJSON(t, HandleTelemetryTracks, "/api/telemetry/tracks", &got)
	if len(got.Clips) != 3 {
		t.Fatalf("clips = %d, want 3 with tracks", len(got.Clips))
	}
	for _, clip := range got.Clips {
		if len(clip.Track) == 0 || clip.Line != nil || clip.Pack != nil {
			t.Fatalf("clip %s: %+v", clip.ID, clip)
		}
	}
}

func TestClipEndpointReadsPackedTrack(t *testing.T) {
	fixtureStore(t)
	var got TelemetryClip
	url := "/api/telemetry/clip?id=SavedClips/2026-11-10_09-31-00/2026-11-10_09-30-00-front.mp4"
	if code := getJSON(t, HandleTelemetryClip, url, &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(got.Track) != 2 || got.Track[0].Lat != float64(float32(54.1)) || got.Track[1].Seconds != 58 {
		t.Fatalf("track = %+v", got.Track)
	}
}

func TestTelemetryDaysSummarisesDrivingTime(t *testing.T) {
	fixtureStore(t)
	var got telemetryDaysResponse
	if code := getJSON(t, HandleTelemetryDays, "/api/telemetry/days", &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(got.Days) != 2 || got.TotalClips != 3 || got.TotalBytes == 0 {
		t.Fatalf("days = %+v", got)
	}
	first := got.Days[0]
	if first.Day != "2026-11-10" || first.Clips != 2 || first.Saved != 1 || first.Recent != 1 ||
		first.DrivingSeconds != 118 || first.First != "2026-11-10T08:00:00" || first.Last != "2026-11-10T09:30:00" {
		t.Fatalf("first day = %+v", first)
	}
	if math.Abs(first.HourMinutes[8]-59.0/60) > 1e-9 || math.Abs(first.HourMinutes[9]-59.0/60) > 1e-9 {
		t.Fatalf("hours = %v", first.HourMinutes)
	}
}

func TestLegacyClipsJSONWinsWhileItExists(t *testing.T) {
	fixtureStore(t)
	writeTelemetryFixture(t, "available") // clips.json with one 2026-11-07 Saved event
	clips, err := loadTelemetryMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 1 || clips[0].ID != "SavedClips/2026-11-07_10-00-00" {
		t.Fatalf("clips = %+v", clips)
	}
}

func TestClearWaitsForTheIndexerLock(t *testing.T) {
	fixtureStore(t)
	lock, err := os.OpenFile(filepath.Join(telemetryStoreDir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	done := make(chan int)
	go func() {
		response := httptest.NewRecorder()
		HandleTelemetryClear(response, httptest.NewRequest(http.MethodPost, "/api/telemetry/clear", nil))
		done <- response.Code
	}()
	select {
	case code := <-done:
		t.Fatalf("clear finished (%d) while the indexer held the lock", code)
	case <-time.After(500 * time.Millisecond):
	}
	if _, err := os.Stat(telemetryDaysDir()); err != nil {
		t.Fatalf("store touched while locked: %v", err)
	}
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("clear status %d", code)
	}
	entries, _ := os.ReadDir(telemetryStoreDir)
	if len(entries) != 1 || entries[0].Name() != ".lock" {
		t.Fatalf("left behind: %v", entries)
	}
}

func TestSimplifyLineMatchesPythonRule(t *testing.T) {
	var track []TelemetryPoint
	for i := 0; i < 120; i++ {
		track = append(track, TelemetryPoint{Seconds: float64(i), Lat: 53 + float64(i)*0.0001, Lon: 18})
	}
	line := simplifyLine(track)
	if len(line) < 4 || len(line) > 6 {
		t.Fatalf("straight 2-minute line kept %d points, want ends plus one every 30 s", len(line))
	}
	if !strings.HasPrefix(normalizeMust(t, "2026-11-10"), "2026-11-10T00:00:00") {
		t.Fatal("day bound")
	}
}

func normalizeMust(t *testing.T, value string) string {
	t.Helper()
	got, err := normalizeRangeBound(value)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestReadsStoreWrittenByPython is driven by
// tests/python/test_telemetry_contract.py, which builds and compacts a store
// with telemetry_index.py and lists what the API must return.
func TestReadsStoreWrittenByPython(t *testing.T) {
	store := os.Getenv("TELEMETRY_PY_STORE")
	if store == "" {
		t.Skip("run through tests/python/test_telemetry_contract.py")
	}
	redirectTelemetryPaths(t)
	telemetryStoreDir = store
	teslaCamRoot = os.Getenv("TELEMETRY_PY_TESLACAM")
	if err := os.WriteFile(confFilePath, []byte("export TELEMETRY_MAP_ENABLED=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var expect struct {
		Clips map[string]struct {
			Points   int        `json:"points"`
			First    [2]float64 `json:"first"`
			Packed   bool       `json:"packed"`
			MinLine  int        `json:"min_line"`
			MaxLine  int        `json:"max_line"`
			Recorded string     `json:"recorded_at"`
		} `json:"clips"`
		Days int `json:"days"`
	}
	data, err := os.ReadFile(os.Getenv("TELEMETRY_PY_EXPECT"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &expect); err != nil {
		t.Fatal(err)
	}
	var lines struct{ Clips []TelemetryClip }
	if code := getJSON(t, HandleTelemetryTracks, "/api/telemetry/tracks?from=2000-01-01", &lines); code != http.StatusOK {
		t.Fatalf("tracks status %d", code)
	}
	if len(lines.Clips) != len(expect.Clips) {
		t.Fatalf("got %d clips, want %d", len(lines.Clips), len(expect.Clips))
	}
	for _, clip := range lines.Clips {
		want, ok := expect.Clips[clip.ID]
		if !ok {
			t.Fatalf("unexpected clip %s", clip.ID)
		}
		if len(clip.Line) < want.MinLine || len(clip.Line) > want.MaxLine {
			t.Fatalf("%s: line has %d points, want %d..%d", clip.ID, len(clip.Line), want.MinLine, want.MaxLine)
		}
		var full TelemetryClip
		if code := getJSON(t, HandleTelemetryClip, "/api/telemetry/clip?id="+clip.ID, &full); code != http.StatusOK {
			t.Fatalf("%s: clip status %d", clip.ID, code)
		}
		if len(full.Track) != want.Points ||
			math.Abs(full.Track[0].Lat-want.First[0]) > 1e-4 || math.Abs(full.Track[0].Lon-want.First[1]) > 1e-4 {
			t.Fatalf("%s: track %d points starting %v, want %d starting %v",
				clip.ID, len(full.Track), full.Track[0], want.Points, want.First)
		}
	}
	var days telemetryDaysResponse
	getJSON(t, HandleTelemetryDays, "/api/telemetry/days", &days)
	if len(days.Days) != expect.Days {
		t.Fatalf("days = %d, want %d", len(days.Days), expect.Days)
	}
}
