package main

// "Street View" from the dashcam: which drives passed near a point, and a
// still from any camera at that moment. Used by the map's Look around mode
// and by the Telegram bot's /near (which calls these endpoints).
//
// Frames come from a static ffmpeg in /backingfiles/tools (run/install-ffmpeg.sh;
// the root filesystem is read-only and too small on a Pi Zero). Only clips
// still on the Pi can give frames; archived ones are listed without cameras.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ffmpegPath          = "/backingfiles/tools/ffmpeg/ffmpeg"
	installFFmpegScript = "/root/bin/install-ffmpeg.sh"
	frameCacheDir       = filepath.Join(os.TempDir(), "teslausb-frames")
	// The cache lives on tmpfs (RAM), so keep it small: about 100 frames.
	frameCacheMaxBytes int64 = 24 << 20
	// One ffmpeg per core at most; a Pi Zero W has one.
	frameSlots = make(chan struct{}, runtime.NumCPU())
	installMu  sync.Mutex
)

var streetCameras = []string{"front", "back", "left_repeater", "right_repeater", "left_pillar", "right_pillar"}

const (
	streetDefaultRadius = 100.0
	streetMaxRadius     = 5000.0
	streetPassGap       = 120 * time.Second
	streetDefaultLimit  = 8
)

// StreetPass is one time the car passed near the point: the closest moment.
type StreetPass struct {
	ID         string   `json:"id"`
	RecordedAt string   `json:"recorded_at"`
	At         string   `json:"at"`
	T          float64  `json:"t"`
	Duration   float64  `json:"duration"`
	DistanceM  int      `json:"distance_m"`
	SpeedKmh   int      `json:"speed_kmh"`
	Heading    float64  `json:"heading"`
	Lat        float64  `json:"lat"`
	Lon        float64  `json:"lon"`
	Saved      bool     `json:"saved"`
	Cameras    []string `json:"cameras"`
	start, end time.Time
}

func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	rad := math.Pi / 180
	dLat, dLon := (lat2-lat1)*rad, (lon2-lon1)*rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * 6371000 * math.Asin(math.Min(1, math.Sqrt(h)))
}

// clipCameraPath is the video of one camera for a minute clip under the
// TeslaCam root, or "" for unknown cameras, legacy whole-event ids and
// anything that is not a plain relative path.
func clipCameraPath(clip TelemetryClip, camera string) string {
	known := false
	for _, name := range streetCameras {
		known = known || name == camera
	}
	relative := clip.Source
	if relative == "" {
		relative = clip.ID
	}
	if !known || !strings.HasSuffix(relative, "-front.mp4") || !filepath.IsLocal(relative) {
		return ""
	}
	return filepath.Join(teslaCamRoot, strings.TrimSuffix(relative, "front.mp4")+camera+".mp4")
}

func availableCameras(clip TelemetryClip) []string {
	cameras := []string{}
	for _, camera := range streetCameras {
		if path := clipCameraPath(clip, camera); path != "" {
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				cameras = append(cameras, camera)
			}
		}
	}
	return cameras
}

// streetNear returns the passes within radius metres of a point, newest
// first. A clip's bounding box (written by the indexer) rules it out without
// reading its track; clips without one are read.
func streetNear(lat, lon, radius float64, limit int) ([]StreetPass, error) {
	clips, err := readTelemetryDayFiles("", "")
	if err != nil {
		return nil, err
	}
	dLat := radius / 110540 * 1.1
	dLon := radius / (111320 * math.Max(0.01, math.Cos(lat*math.Pi/180))) * 1.1
	hits := []StreetPass{}
	for _, clip := range clips {
		if clip.Samples == 0 {
			continue
		}
		if len(clip.BBox) == 4 && (lat < clip.BBox[0]-dLat || lat > clip.BBox[2]+dLat || lon < clip.BBox[1]-dLon || lon > clip.BBox[3]+dLon) {
			continue
		}
		track, err := readClipTrack(clip)
		if err != nil || len(track) == 0 {
			continue
		}
		best, bestDistance := track[0], math.Inf(1)
		for _, point := range track {
			if d := haversineMeters(lat, lon, point.Lat, point.Lon); d < bestDistance {
				best, bestDistance = point, d
			}
		}
		if bestDistance > radius {
			continue
		}
		start, err := time.Parse("2006-01-02T15:04:05", recordedLocal(clip))
		if err != nil {
			continue
		}
		duration := clip.Duration
		if duration <= 0 {
			duration = 60
		}
		hits = append(hits, StreetPass{
			ID: clip.ID, RecordedAt: recordedLocal(clip),
			At: start.Add(time.Duration(best.Seconds * float64(time.Second))).Format("2006-01-02T15:04:05"),
			T:  math.Round(best.Seconds*10) / 10, Duration: duration,
			DistanceM: int(math.Round(bestDistance)), SpeedKmh: int(math.Round(best.Speed * 3.6)),
			Heading: math.Round(best.Heading), Lat: best.Lat, Lon: best.Lon,
			Saved: strings.HasPrefix(clip.ID, "SavedClips/"),
			start: start, end: start.Add(time.Duration(duration * float64(time.Second))),
		})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].start.Before(hits[j].start) })
	// Consecutive minutes of one drive are one pass: keep its closest moment.
	passes := []StreetPass{}
	for _, hit := range hits {
		if n := len(passes); n > 0 && hit.start.Sub(passes[n-1].end) <= streetPassGap {
			end := hit.end
			if hit.DistanceM < passes[n-1].DistanceM {
				passes[n-1] = hit
			}
			passes[n-1].end = end
			continue
		}
		passes = append(passes, hit)
	}
	sort.Slice(passes, func(i, j int) bool { return passes[i].start.After(passes[j].start) })
	if limit > 0 && len(passes) > limit {
		passes = passes[:limit]
	}
	byID := map[string]TelemetryClip{}
	for _, clip := range clips {
		byID[clip.ID] = clip
	}
	for i := range passes {
		passes[i].Cameras = availableCameras(byID[passes[i].ID])
	}
	return passes, nil
}

func ffmpegInstalled() bool {
	info, err := os.Stat(ffmpegPath)
	return err == nil && info.Mode().IsRegular()
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// HandleStreetNear lists the passes near a point.
// GET /api/street/near?lat=&lon=[&radius=metres][&limit=n]
func HandleStreetNear(w http.ResponseWriter, r *http.Request) {
	if !readTelemetrySettings().Enabled {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "GPS track history is disabled"})
		return
	}
	query := r.URL.Query()
	lat, errLat := strconv.ParseFloat(query.Get("lat"), 64)
	lon, errLon := strconv.ParseFloat(query.Get("lon"), 64)
	radius := streetDefaultRadius
	if value := query.Get("radius"); value != "" {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			errLat = err
		}
		radius = parsed
	}
	limit := streetDefaultLimit
	if value := query.Get("limit"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}
	if errLat != nil || errLon != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 || radius < 1 || radius > streetMaxRadius {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "lat, lon or radius (1-5000 m) is invalid"})
		return
	}
	passes, err := streetNear(lat, lon, radius, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read telemetry history"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"lat": lat, "lon": lon, "radius": radius, "passes": passes, "ffmpeg": ffmpegInstalled(),
	})
}

func findTelemetryClip(id string) (TelemetryClip, bool) {
	clips, err := readTelemetryDayFiles("", "")
	if err != nil {
		return TelemetryClip{}, false
	}
	for _, clip := range clips {
		if clip.ID == id {
			return clip, true
		}
	}
	return TelemetryClip{}, false
}

// pruneFrameCache keeps the cache under its size limit, oldest first.
func pruneFrameCache() {
	entries, err := os.ReadDir(frameCacheDir)
	if err != nil {
		return
	}
	type cached struct {
		path string
		size int64
		mod  time.Time
	}
	files := []cached{}
	var total int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, cached{filepath.Join(frameCacheDir, entry.Name()), info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, file := range files {
		if total <= frameCacheMaxBytes {
			break
		}
		if os.Remove(file.path) == nil {
			total -= file.size
		}
	}
}

// renderFrame returns the path of a cached JPEG of one camera at t seconds.
func renderFrame(ctx context.Context, clip TelemetryClip, camera string, t float64, width int) (string, error) {
	source := clipCameraPath(clip, camera)
	if source == "" {
		return "", os.ErrNotExist
	}
	info, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	duration := clip.Duration
	if duration <= 0 {
		duration = 60
	}
	t = math.Max(0, math.Min(t, duration-0.5))
	t = math.Round(t*10) / 10
	key := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%.1f|%d|%d|%d", clip.ID, camera, t, width, info.Size(), info.ModTime().UnixNano())))
	target := filepath.Join(frameCacheDir, hex.EncodeToString(key[:16])+".jpg")
	if cachedInfo, err := os.Stat(target); err == nil && cachedInfo.Size() > 0 {
		now := time.Now()
		_ = os.Chtimes(target, now, now) // most recently used survives pruning
		return target, nil
	}
	if err := os.MkdirAll(frameCacheDir, 0o700); err != nil {
		return "", err
	}
	select {
	case frameSlots <- struct{}{}:
		defer func() { <-frameSlots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	temporary := target + ".tmp"
	args := []string{"-nostdin", "-loglevel", "error", "-ss", strconv.FormatFloat(t, 'f', 1, 64), "-i", source, "-frames:v", "1", "-q:v", "4"}
	if width > 0 {
		args = append(args, "-vf", fmt.Sprintf("scale=%d:-2", width))
	}
	args = append(args, "-f", "image2", "-y", temporary)
	runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	output, err := exec.CommandContext(runCtx, ffmpegPath, args...).CombinedOutput()
	if err != nil {
		_ = os.Remove(temporary)
		return "", fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(string(output)))
	}
	if err := os.Rename(temporary, target); err != nil {
		return "", err
	}
	pruneFrameCache()
	return target, nil
}

// HandleStreetFrame returns one camera's still at a moment of a clip.
// GET /api/street/frame?id=<clip id>&camera=front&t=<seconds>[&w=640]
func HandleStreetFrame(w http.ResponseWriter, r *http.Request) {
	if !readTelemetrySettings().Enabled {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "GPS track history is disabled"})
		return
	}
	query := r.URL.Query()
	t, err := strconv.ParseFloat(query.Get("t"), 64)
	width := 0
	if value := query.Get("w"); value != "" {
		switch value {
		case "320", "480", "640", "960":
			width, _ = strconv.Atoi(value)
		default:
			err = errors.New("w must be 320, 480, 640 or 960")
		}
	}
	if err != nil || math.IsNaN(t) || math.IsInf(t, 0) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "t (seconds) or w is invalid"})
		return
	}
	clip, ok := findTelemetryClip(query.Get("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such clip"})
		return
	}
	if !ffmpegInstalled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "ffmpeg is not installed", "install": true})
		return
	}
	path, err := renderFrame(r.Context(), clip, query.Get("camera"), t, width)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "that camera's video is no longer on the Pi"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, path)
}

// HandleStreetInstallFFmpeg installs the static ffmpeg (one download of about
// 15-20 MB, checked against a pinned SHA-256).
// POST /api/street/install-ffmpeg
func HandleStreetInstallFFmpeg(w http.ResponseWriter, r *http.Request) {
	installMu.Lock()
	defer installMu.Unlock()
	if ffmpegInstalled() {
		writeJSON(w, http.StatusOK, map[string]any{"installed": true, "path": ffmpegPath})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/bin/bash", installFFmpegScript).CombinedOutput()
	if err != nil || !ffmpegInstalled() {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"installed": false, "error": strings.TrimSpace(string(output))})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"installed": true, "path": ffmpegPath})
}
