package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// streetFixture: three minutes past a point P (53.80, -9.55) on 12 Nov:
// 10:00 and 10:01 are one drive (one pass), 10:30 a second one. A fourth
// clip far away has a bounding box, so it is never read.
func streetFixture(t *testing.T) {
	t.Helper()
	redirectTelemetryPaths(t)
	if err := os.WriteFile(confFilePath, []byte("export TELEMETRY_MAP_ENABLED=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	clips := []TelemetryClip{}
	add := func(stamp string, lat0 float32, step float32) {
		id := "RecentClips/2026-11-12/2026-11-12_" + stamp + "-front.mp4"
		clips = append(clips, TelemetryClip{ID: id, RecordedAt: "2026-11-12T" + strings.ReplaceAll(stamp, "-", ":"),
			State: "available", Duration: 59, Samples: 3, PointWords: 15})
		writeRawTrack(t, id, point15(0, lat0, -9.55), point15(30, lat0+step, -9.55), point15(59, lat0+2*step, -9.55))
	}
	add("10-00-00", 53.7990, 0.0004)  // reaches P at t=30 (about 45 m short)
	add("10-01-00", 53.7999, 0.00005) // reaches P exactly at t=59: closest of the drive
	add("10-30-00", 53.8003, 0.001)   // starts 33 m from P
	far := TelemetryClip{ID: "RecentClips/2026-11-12/2026-11-12_12-00-00-front.mp4", RecordedAt: "2026-11-12T12:00:00",
		State: "available", Duration: 59, Samples: 3, PointWords: 15, BBox: []float64{51, 0, 51.1, 0.1}}
	clips = append(clips, far) // no track file: reading it would be skipped anyway, the box must rule it out
	writeDayFile(t, "2026-11-12", clips)
	// Videos on the Pi: front and back for 10:01, nothing for 10:30.
	for _, camera := range []string{"front", "back"} {
		path := filepath.Join(teslaCamRoot, "RecentClips/2026-11-12/2026-11-12_10-01-00-"+camera+".mp4")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("video"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStreetNearMergesOneDriveAndListsCameras(t *testing.T) {
	streetFixture(t)
	var got struct {
		Passes []StreetPass
		FFmpeg bool
	}
	if code := getJSON(t, HandleStreetNear, "/api/street/near?lat=53.80&lon=-9.55&radius=100", &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(got.Passes) != 2 {
		t.Fatalf("want 2 passes (10:30, then the 10:00-10:01 drive), got %+v", got.Passes)
	}
	newest, drive := got.Passes[0], got.Passes[1]
	if newest.ID != "RecentClips/2026-11-12/2026-11-12_10-30-00-front.mp4" || len(newest.Cameras) != 0 {
		t.Fatalf("newest pass = %+v", newest)
	}
	if drive.ID != "RecentClips/2026-11-12/2026-11-12_10-01-00-front.mp4" || drive.DistanceM != 0 || drive.T != 59 {
		t.Fatalf("drive's closest moment = %+v", drive)
	}
	if strings.Join(drive.Cameras, ",") != "front,back" || drive.At != "2026-11-12T10:01:59" {
		t.Fatalf("drive cameras/time = %+v", drive)
	}
	if code := getJSON(t, HandleStreetNear, "/api/street/near?lat=53.80&lon=-9.5507&radius=20", &got); code != http.StatusOK || len(got.Passes) != 0 {
		t.Fatalf("46 m off the road, 20 m radius: %d %+v", code, got.Passes)
	}
	for _, bad := range []string{"lat=91&lon=0", "lat=53&lon=x", "lat=53&lon=-1&radius=0", "lat=53&lon=-1&radius=99999"} {
		if code := getJSON(t, HandleStreetNear, "/api/street/near?"+bad, &got); code != http.StatusBadRequest {
			t.Fatalf("%s gave %d", bad, code)
		}
	}
}

func fakeFFmpeg(t *testing.T) (counter string) {
	t.Helper()
	dir := t.TempDir()
	counter = filepath.Join(dir, "runs")
	script := filepath.Join(dir, "ffmpeg")
	body := "#!/bin/sh\necho \"$@\" >> " + counter + "\nfor last; do :; done\nprintf 'JPEGDATA' > \"$last\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	originalPath, originalCache := ffmpegPath, frameCacheDir
	ffmpegPath, frameCacheDir = script, filepath.Join(dir, "cache")
	t.Cleanup(func() { ffmpegPath, frameCacheDir = originalPath, originalCache })
	return counter
}

func getRaw(handler http.HandlerFunc, url string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler(response, httptest.NewRequest(http.MethodGet, url, nil))
	return response
}

func TestStreetFrameRendersOnceAndCaches(t *testing.T) {
	streetFixture(t)
	counter := fakeFFmpeg(t)
	url := "/api/street/frame?id=RecentClips/2026-11-12/2026-11-12_10-01-00-front.mp4&camera=back&t=80&w=640"
	for i := 0; i < 2; i++ {
		response := getRaw(HandleStreetFrame, url)
		if response.Code != http.StatusOK || response.Body.String() != "JPEGDATA" || response.Header().Get("Content-Type") != "image/jpeg" {
			t.Fatalf("request %d: %d %q", i, response.Code, response.Body.String())
		}
	}
	runs, _ := os.ReadFile(counter)
	lines := strings.Split(strings.TrimSpace(string(runs)), "\n")
	if len(lines) != 1 {
		t.Fatalf("ffmpeg ran %d times, want once (second from cache)", len(lines))
	}
	// t=80 is past the end of a 59 s clip: clamped to 58.5 s; the 640 px width is passed on.
	if !strings.Contains(lines[0], "-ss 58.5 ") || !strings.Contains(lines[0], "scale=640:-2") || !strings.Contains(lines[0], "10-01-00-back.mp4") {
		t.Fatalf("ffmpeg args: %s", lines[0])
	}
	if code := getRaw(HandleStreetFrame, strings.Replace(url, "camera=back", "camera=left_repeater", 1)).Code; code != http.StatusNotFound {
		t.Fatalf("missing camera video gave %d", code)
	}
	if code := getRaw(HandleStreetFrame, strings.Replace(url, "camera=back", "camera=../../etc", 1)).Code; code != http.StatusNotFound {
		t.Fatalf("bad camera gave %d", code)
	}
	if code := getRaw(HandleStreetFrame, "/api/street/frame?id=nope&camera=front&t=1").Code; code != http.StatusNotFound {
		t.Fatalf("unknown clip gave %d", code)
	}
	if code := getRaw(HandleStreetFrame, strings.Replace(url, "w=640", "w=4000", 1)).Code; code != http.StatusBadRequest {
		t.Fatalf("bad width gave %d", code)
	}
}

func TestStreetFrameNeedsFFmpegAndInstallsIt(t *testing.T) {
	streetFixture(t)
	dir := t.TempDir()
	originalPath, originalScript := ffmpegPath, installFFmpegScript
	ffmpegPath = filepath.Join(dir, "tools/ffmpeg/ffmpeg")
	installFFmpegScript = filepath.Join(dir, "install.sh")
	t.Cleanup(func() { ffmpegPath, installFFmpegScript = originalPath, originalScript })
	url := "/api/street/frame?id=RecentClips/2026-11-12/2026-11-12_10-01-00-front.mp4&camera=front&t=1"
	response := getRaw(HandleStreetFrame, url)
	var body map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != http.StatusServiceUnavailable || body["install"] != true {
		t.Fatalf("without ffmpeg: %d %v", response.Code, body)
	}
	script := "#!/bin/bash\nmkdir -p " + filepath.Dir(ffmpegPath) + "\ntouch " + ffmpegPath + "\n"
	if err := os.WriteFile(installFFmpegScript, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	install := httptest.NewRecorder()
	HandleStreetInstallFFmpeg(install, httptest.NewRequest(http.MethodPost, "/api/street/install-ffmpeg", nil))
	if install.Code != http.StatusOK || !ffmpegInstalled() {
		t.Fatalf("install: %d %s", install.Code, install.Body.String())
	}
}

func TestStreetEndpointsNeedTelemetryEnabled(t *testing.T) {
	streetFixture(t)
	if err := os.WriteFile(confFilePath, []byte("export TELEMETRY_MAP_ENABLED=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := getRaw(HandleStreetNear, "/api/street/near?lat=53.8&lon=-9.55").Code; code != http.StatusNotFound {
		t.Fatalf("near gave %d", code)
	}
	if code := getRaw(HandleStreetFrame, "/api/street/frame?id=x&camera=front&t=1").Code; code != http.StatusNotFound {
		t.Fatalf("frame gave %d", code)
	}
}
