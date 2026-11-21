package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
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
	telemetryStoreDir = "/mutable/telemetry"
	teslaCamRoot      = "/mutable/TeslaCam"
	runTelemetryPrune = func(retentionDays int) error {
		cmd := exec.Command("/root/bin/telemetry_index.py", "prune")
		cmd.Env = append(os.Environ(), "TELEMETRY_RETENTION_DAYS="+strconv.Itoa(retentionDays))
		return cmd.Run()
	}
)

const defaultTelemetryTileURL = "https://tile.openstreetmap.org/{z}/{x}/{y}.png"

type TelemetryPoint struct {
	Seconds      float64 `json:"t"`
	Lat          float64 `json:"lat"`
	Lon          float64 `json:"lon"`
	Speed        float64 `json:"speed"`
	Heading      float64 `json:"heading"`
	Gear         int     `json:"gear"`
	Autopilot    int     `json:"autopilot"`
	AccelX       float64 `json:"accel_x"`
	AccelY       float64 `json:"accel_y"`
	AccelZ       float64 `json:"accel_z"`
	Accelerator  float64 `json:"accelerator"`
	Brake        bool    `json:"brake"`
	Steering     float64 `json:"steering"`
	BlinkerLeft  bool    `json:"blinker_left"`
	BlinkerRight bool    `json:"blinker_right"`
}

type TelemetryClip struct {
	ID         string           `json:"id"`
	RecordedAt string           `json:"recorded_at"`
	State      string           `json:"state"`
	FrontFile  string           `json:"front_file"`
	Source     string           `json:"source"`
	Duration   float64          `json:"duration"`
	Samples    int              `json:"samples"`
	PointWords int              `json:"point_words"`
	Extended   bool             `json:"extended"`
	TileURL    string           `json:"tile_url,omitempty"`
	StyleURL   string           `json:"style_url,omitempty"`
	Track      []TelemetryPoint `json:"track,omitempty"`
	// Line is the simplified [lat, lon] map line returned by ranged
	// /api/telemetry/tracks requests instead of the full track.
	Line [][2]float64 `json:"line,omitempty"`
	// Pack locates a compacted clip; internal, cleared before responding.
	Pack *TelemetryPack `json:"pack,omitempty"`
	// BBox is [min lat, min lon, max lat, max lon] for place searches.
	BBox []float64 `json:"bbox,omitempty"`
}

type TelemetrySettings struct {
	Enabled       bool   `json:"enabled"`
	RetentionDays int    `json:"retention_days"`
	TileURL       string `json:"tile_url"`
	StyleURL      string `json:"style_url"`
	// WhileDriving (TELEMETRY_WHILE_DRIVING, experimental) indexes new clips
	// after every snapshot, not only in the archive cycle. Nil in a request
	// keeps the saved value, so older clients do not switch it off.
	WhileDriving *bool `json:"while_driving,omitempty"`
}

func readTelemetrySettings() TelemetrySettings {
	settings := TelemetrySettings{RetentionDays: 30, TileURL: defaultTelemetryTileURL}
	data, err := os.ReadFile(confFilePath)
	if err != nil {
		return settings
	}
	content := string(data)
	settings.Enabled = readConfKey(content, "TELEMETRY_MAP_ENABLED") == "true"
	if value := readConfKey(content, "TELEMETRY_RETENTION_DAYS"); value != "" {
		if days, err := strconv.Atoi(value); err == nil && days >= 1 && days <= 3650 {
			settings.RetentionDays = days
		}
	}
	if value := readConfKey(content, "TELEMETRY_TILE_URL"); value != "" {
		settings.TileURL = value
	}
	settings.StyleURL = readConfKey(content, "TELEMETRY_MAP_STYLE_URL")
	whileDriving := readConfKey(content, "TELEMETRY_WHILE_DRIVING") == "true"
	settings.WhileDriving = &whileDriving
	return settings
}

func writeTelemetrySettings(settings TelemetrySettings) error {
	return withConfRW(func() error {
		data, err := os.ReadFile(confFilePath)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		content := upsertConfKey(string(data), "TELEMETRY_MAP_ENABLED", strconv.FormatBool(settings.Enabled))
		content = upsertConfKey(content, "TELEMETRY_RETENTION_DAYS", strconv.Itoa(settings.RetentionDays))
		content = upsertConfKey(content, "TELEMETRY_TILE_URL", settings.TileURL)
		content = upsertConfKey(content, "TELEMETRY_MAP_STYLE_URL", settings.StyleURL)
		if settings.WhileDriving != nil {
			content = upsertConfKey(content, "TELEMETRY_WHILE_DRIVING", strconv.FormatBool(*settings.WhileDriving))
		}
		return writeFileAtomic(confFilePath, []byte(content), 0o600)
	})
}

func telemetryTrackPath(id string) (string, error) {
	parts := strings.Split(id, "/")
	validCategory := (len(parts) == 2 && parts[0] == "SavedClips") ||
		(len(parts) == 3 && (parts[0] == "SavedClips" || parts[0] == "RecentClips"))
	if !validCategory {
		return "", errors.New("invalid clip id")
	}
	for _, part := range parts[1:] {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("invalid clip id")
		}
	}
	digest := sha256.Sum256([]byte(id))
	return filepath.Join(telemetryStoreDir, "tracks", hex.EncodeToString(digest[:])+".f32"), nil
}

type cachedTelemetryTrack struct {
	modNano int64
	size    int64
	points  []TelemetryPoint
}

var telemetryTrackCache = struct {
	sync.Mutex
	tracks map[string]cachedTelemetryTrack
}{tracks: map[string]cachedTelemetryTrack{}}

func readTelemetryTrack(id string, pointWords int) ([]TelemetryPoint, error) {
	path, err := telemetryTrackPath(id)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if pointWords == 0 {
		pointWords = 4
	}
	cacheKey := path + ":" + strconv.Itoa(pointWords)
	telemetryTrackCache.Lock()
	if cached, ok := telemetryTrackCache.tracks[cacheKey]; ok && cached.modNano == info.ModTime().UnixNano() && cached.size == info.Size() {
		points := cached.points
		telemetryTrackCache.Unlock()
		return points, nil
	}
	telemetryTrackCache.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	points, err := decodeTelemetryTrack(data, pointWords)
	if err != nil {
		return nil, err
	}
	telemetryTrackCache.Lock()
	telemetryTrackCache.tracks[cacheKey] = cachedTelemetryTrack{modNano: info.ModTime().UnixNano(), size: info.Size(), points: points}
	telemetryTrackCache.Unlock()
	return points, nil
}

// decodeTelemetryTrack decodes packed little-endian float32 points (4 or 15
// words each), skipping points with non-finite values.
func decodeTelemetryTrack(data []byte, pointWords int) ([]TelemetryPoint, error) {
	if pointWords != 4 && pointWords != 15 {
		return nil, errors.New("unsupported telemetry track format")
	}
	stride := pointWords * 4
	if len(data)%stride != 0 {
		return nil, errors.New("truncated telemetry track")
	}
	points := make([]TelemetryPoint, 0, len(data)/stride)
	for offset := 0; offset < len(data); offset += stride {
		values := [15]float64{}
		finite := true
		for i := 0; i < pointWords; i++ {
			bits := binary.LittleEndian.Uint32(data[offset+i*4:])
			values[i] = float64(math.Float32frombits(bits))
			if math.IsNaN(values[i]) || math.IsInf(values[i], 0) {
				finite = false
				break
			}
		}
		if !finite {
			continue
		}
		point := TelemetryPoint{Seconds: values[0], Lat: values[1], Lon: values[2], Speed: values[3]}
		if pointWords == 15 {
			point.Heading = values[4]
			point.Gear = int(values[5])
			point.Autopilot = int(values[6])
			point.AccelX, point.AccelY, point.AccelZ = values[7], values[8], values[9]
			point.Accelerator = values[10]
			point.Brake = values[11] != 0
			point.Steering = values[12]
			point.BlinkerLeft, point.BlinkerRight = values[13] != 0, values[14] != 0
		}
		points = append(points, point)
	}
	return points, nil
}

func loadTelemetryMetadata() ([]TelemetryClip, error) {
	return loadTelemetryMetadataRange("", "")
}

// loadTelemetryMetadataRange reads the clips of days in [fromDay, toDay] and
// sets each one's state from whether its video is still on the Pi.
func loadTelemetryMetadataRange(fromDay, toDay string) ([]TelemetryClip, error) {
	stored, err := readTelemetryDayFiles(fromDay, toDay)
	if err != nil {
		return nil, err
	}
	clips := make([]TelemetryClip, 0, len(stored))
	for _, clip := range stored {
		if _, err := telemetryTrackPath(clip.ID); err != nil {
			continue
		}
		relativePath := clip.Source
		if strings.Split(clip.ID, "/")[0] == "SavedClips" && len(strings.Split(clip.ID, "/")) == 2 {
			relativePath = filepath.Join(clip.ID, clip.FrontFile)
		} else if relativePath == "" {
			relativePath = clip.ID
		}
		if !filepath.IsLocal(relativePath) {
			continue
		}
		clipPath := filepath.Join(teslaCamRoot, relativePath)
		info, statErr := os.Stat(clipPath)
		switch {
		case statErr == nil && info.Mode().IsRegular():
			clip.State = "available"
		case clip.State != "archived":
			clip.State = "overwritten"
		}
		clips = append(clips, clip)
	}
	return clips, nil
}

func loadTelemetryClips() ([]TelemetryClip, error) {
	clips, err := loadTelemetryMetadata()
	if err != nil {
		return nil, err
	}
	loaded := make([]TelemetryClip, 0, len(clips))
	for _, clip := range clips {
		track, err := readClipTrack(clip)
		if err != nil {
			continue
		}
		clip.Extended = clip.PointWords == 15
		clip.Track = track
		clip.Pack = nil
		loaded = append(loaded, clip)
	}
	return loaded, nil
}

// loadTelemetryLines returns the clips recorded in [from, to) (local
// "YYYY-MM-DDTHH:MM:SS" bounds, either may be empty) with their simplified
// map lines, or with full tracks when full is set (trip replay).
func loadTelemetryLines(from, to string, full bool) ([]TelemetryClip, error) {
	fromDay, toDay := "", ""
	if from != "" {
		fromDay = from[:10]
	}
	if to != "" {
		toDay = to[:10]
	}
	clips, err := loadTelemetryMetadataRange(fromDay, toDay)
	if err != nil {
		return nil, err
	}
	loaded := make([]TelemetryClip, 0, len(clips))
	for _, clip := range clips {
		local := recordedLocal(clip)
		if (from != "" && local < from) || (to != "" && local >= to) || clip.Samples == 0 {
			continue
		}
		if full {
			track, err := readClipTrack(clip)
			if err != nil || len(track) == 0 {
				continue
			}
			clip.Track = track
		} else {
			line, err := readClipLine(clip)
			if err != nil || len(line) == 0 {
				continue
			}
			clip.Line = line
		}
		clip.Extended = clip.PointWords == 15
		clip.Pack = nil
		loaded = append(loaded, clip)
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].RecordedAt < loaded[j].RecordedAt })
	return loaded, nil
}

// shortRange reports whether both bounds are set and at most limit apart.
func shortRange(from, to string, limit time.Duration) bool {
	start, errFrom := time.Parse("2006-01-02T15:04:05", from)
	end, errTo := time.Parse("2006-01-02T15:04:05", to)
	return errFrom == nil && errTo == nil && end.Sub(start) <= limit && !end.Before(start)
}

// HandleTelemetryTracks returns the track history for the map.
//
// With from and/or to (YYYY-MM-DD or YYYY-MM-DDTHH:MM, the car's local time),
// only clips recorded in [from, to) are returned, each with a simplified
// "line" instead of the full "track" (roughly 10x smaller); with full=1 and
// a range of at most 48 hours, full tracks (trip replay). Without them, it
// keeps the original behaviour (every clip, full tracks) for older clients.
// GET /api/telemetry/tracks
func HandleTelemetryTracks(w http.ResponseWriter, r *http.Request) {
	settings := readTelemetrySettings()
	response := struct {
		TelemetrySettings
		From  string          `json:"from,omitempty"`
		To    string          `json:"to,omitempty"`
		Clips []TelemetryClip `json:"clips"`
	}{TelemetrySettings: settings, Clips: []TelemetryClip{}}
	query := r.URL.Query()
	ranged := query.Has("from") || query.Has("to")
	if ranged {
		from, fromErr := normalizeRangeBound(query.Get("from"))
		to, toErr := normalizeRangeBound(query.Get("to"))
		if fromErr != nil || toErr != nil {
			http.Error(w, `{"error":"from and to must be YYYY-MM-DD or YYYY-MM-DDTHH:MM"}`, http.StatusBadRequest)
			return
		}
		response.From, response.To = from, to
		full := query.Get("full") == "1"
		if full && !shortRange(from, to, 48*time.Hour) {
			http.Error(w, `{"error":"full tracks need from and to at most 48 hours apart"}`, http.StatusBadRequest)
			return
		}
		if settings.Enabled {
			clips, err := loadTelemetryLines(from, to, full)
			if err != nil {
				http.Error(w, `{"error":"could not read telemetry history"}`, http.StatusInternalServerError)
				return
			}
			response.Clips = clips
		}
	} else if settings.Enabled {
		clips, err := loadTelemetryClips()
		if err != nil {
			http.Error(w, `{"error":"could not read telemetry history"}`, http.StatusInternalServerError)
			return
		}
		response.Clips = clips
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func HandleTelemetryClip(w http.ResponseWriter, r *http.Request) {
	if !readTelemetrySettings().Enabled {
		http.Error(w, `{"error":"telemetry map disabled"}`, http.StatusNotFound)
		return
	}
	id := r.URL.Query().Get("id")
	clips, err := loadTelemetryMetadata()
	if err != nil {
		http.Error(w, `{"error":"could not read telemetry history"}`, http.StatusInternalServerError)
		return
	}
	eventPrefix := ""
	if strings.HasPrefix(id, "SavedClips/") && strings.Count(id, "/") == 1 {
		eventPrefix = id + "/"
	}
	var prefixMatch *TelemetryClip
	var selected *TelemetryClip
	for i := range clips {
		clip := clips[i]
		if clip.ID == id {
			selected = &clip
			break
		}
		if eventPrefix != "" && strings.HasPrefix(clip.ID, eventPrefix) {
			candidate := clip
			prefixMatch = &candidate
		}
	}
	if selected == nil {
		selected = prefixMatch
	}
	if selected == nil {
		http.Error(w, `{"error":"telemetry not available"}`, http.StatusNotFound)
		return
	}
	track, err := readClipTrack(*selected)
	if err != nil {
		http.Error(w, `{"error":"telemetry not available"}`, http.StatusNotFound)
		return
	}
	selected.Track = track
	selected.Pack = nil
	selected.Extended = selected.PointWords == 15
	settings := readTelemetrySettings()
	selected.TileURL = settings.TileURL
	selected.StyleURL = settings.StyleURL
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(*selected)
}

func HandleTelemetrySettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(readTelemetrySettings())
		return
	}
	var settings TelemetrySettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if settings.RetentionDays < 1 || settings.RetentionDays > 3650 {
		http.Error(w, `{"error":"retention_days must be between 1 and 3650"}`, http.StatusBadRequest)
		return
	}
	if settings.TileURL == "" {
		settings.TileURL = defaultTelemetryTileURL
	}
	parsedURL, err := url.Parse(settings.TileURL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || !strings.Contains(settings.TileURL, "{z}") || !strings.Contains(settings.TileURL, "{x}") || !strings.Contains(settings.TileURL, "{y}") {
		http.Error(w, `{"error":"tile_url must be an HTTP(S) URL containing {z}, {x}, and {y}"}`, http.StatusBadRequest)
		return
	}
	if settings.StyleURL != "" {
		styleURL, err := url.Parse(settings.StyleURL)
		if err != nil || styleURL.Host == "" || (styleURL.Scheme != "http" && styleURL.Scheme != "https") {
			http.Error(w, `{"error":"style_url must be an HTTP(S) URL"}`, http.StatusBadRequest)
			return
		}
	}
	if err := writeTelemetrySettings(settings); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	if err := runTelemetryPrune(settings.RetentionDays); err != nil {
		http.Error(w, `{"error":"settings saved, but pruning old tracks failed"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(readTelemetrySettings())
}

// HandleTelemetryClear deletes every stored track. It takes the indexer's
// lock first, so a running indexer cannot write its clips back afterwards.
func HandleTelemetryClear(w http.ResponseWriter, r *http.Request) {
	err := withTelemetryStoreLock(60*time.Second, func() error {
		entries, err := os.ReadDir(telemetryStoreDir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Name() == ".lock" {
				continue
			}
			if err := os.RemoveAll(filepath.Join(telemetryStoreDir, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		http.Error(w, `{"error":"could not clear telemetry history"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"cleared": true})
}
