package main

// Reading the telemetry store written by run/telemetry_index.py.
//
// Layout (see the docstrings in telemetry_index.py):
//
//	clips.json                 before 2026-11-12: every clip; read while it exists
//	days/<YYYY-MM-DD>.json     the clips recorded that local day
//	days/<day>.<gen>.f32       a finished day's tracks, 15 float32 per point
//	days/<day>.<gen>.line      its simplified map lines, (lat, lon) float32 pairs
//	tracks/<sha256>.f32        raw per-clip tracks until their day is packed

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// TelemetryPack locates a clip inside its day's packed files.
type TelemetryPack struct {
	Name       string `json:"name"`
	Offset     int    `json:"offset"`
	Count      int    `json:"count"`
	LineOffset int    `json:"line_offset"`
	LineCount  int    `json:"line_count"`
}

const telemetryPointFloats = 15

func telemetryDaysDir() string { return filepath.Join(telemetryStoreDir, "days") }

// validDay reports whether s looks like YYYY-MM-DD.
func validDay(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// readTelemetryDayFiles returns the stored clips of the days in [fromDay,
// toDay] (either may be empty for no bound). The legacy clips.json wins while
// it exists, since the indexer removes it only after writing every day file.
func readTelemetryDayFiles(fromDay, toDay string) ([]TelemetryClip, error) {
	var stored struct {
		Clips []TelemetryClip `json:"clips"`
	}
	legacy, err := os.ReadFile(filepath.Join(telemetryStoreDir, "clips.json"))
	if err == nil {
		if err := json.Unmarshal(legacy, &stored); err != nil {
			return nil, err
		}
		clips := stored.Clips[:0]
		for _, clip := range stored.Clips {
			day := clipDay(clip)
			if (fromDay == "" || day >= fromDay) && (toDay == "" || day <= toDay) {
				clips = append(clips, clip)
			}
		}
		return clips, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(telemetryDaysDir(), "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	clips := []TelemetryClip{}
	for _, path := range paths {
		day := strings.TrimSuffix(filepath.Base(path), ".json")
		if !validDay(day) || (fromDay != "" && day < fromDay) || (toDay != "" && day > toDay) {
			continue
		}
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue // removed by retention while we listed
		}
		if err != nil {
			return nil, err
		}
		stored.Clips = nil
		if err := json.Unmarshal(data, &stored); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		clips = append(clips, stored.Clips...)
	}
	return clips, nil
}

func clipDay(clip TelemetryClip) string {
	if len(clip.RecordedAt) >= 10 {
		return clip.RecordedAt[:10]
	}
	return ""
}

// cachedFile keeps a whole packed file in memory until it changes on disk.
type cachedFile struct {
	modNano int64
	size    int64
	data    []byte
}

var packCache = struct {
	sync.Mutex
	files map[string]cachedFile
}{files: map[string]cachedFile{}}

func readCachedFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	packCache.Lock()
	cached, ok := packCache.files[path]
	packCache.Unlock()
	if ok && cached.modNano == info.ModTime().UnixNano() && cached.size == info.Size() {
		return cached.data, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	packCache.Lock()
	if len(packCache.files) > 120 { // about four months of days; drop all and refill
		packCache.files = map[string]cachedFile{}
	}
	packCache.files[path] = cachedFile{modNano: info.ModTime().UnixNano(), size: info.Size(), data: data}
	packCache.Unlock()
	return data, nil
}

func validPackName(name string) bool {
	day, generation, ok := strings.Cut(name, ".")
	if !ok || !validDay(day) || generation == "" {
		return false
	}
	for _, r := range generation {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// readPackedTrack returns a packed clip's points.
func readPackedTrack(pack *TelemetryPack) ([]TelemetryPoint, error) {
	if !validPackName(pack.Name) {
		return nil, errors.New("invalid pack name")
	}
	data, err := readCachedFile(filepath.Join(telemetryDaysDir(), pack.Name+".f32"))
	if err != nil {
		return nil, err
	}
	stride := telemetryPointFloats * 4
	start, end := pack.Offset*stride, (pack.Offset+pack.Count)*stride
	if pack.Offset < 0 || pack.Count < 0 || end > len(data) {
		return nil, errors.New("truncated telemetry pack")
	}
	return decodeTelemetryTrack(data[start:end], telemetryPointFloats)
}

// readPackedLine returns a packed clip's simplified map line.
func readPackedLine(pack *TelemetryPack) ([][2]float64, error) {
	if !validPackName(pack.Name) {
		return nil, errors.New("invalid pack name")
	}
	data, err := readCachedFile(filepath.Join(telemetryDaysDir(), pack.Name+".line"))
	if err != nil {
		return nil, err
	}
	start, end := pack.LineOffset*8, (pack.LineOffset+pack.LineCount)*8
	if pack.LineOffset < 0 || pack.LineCount < 0 || end > len(data) {
		return nil, errors.New("truncated telemetry line")
	}
	line := make([][2]float64, 0, pack.LineCount)
	for offset := start; offset < end; offset += 8 {
		lat := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[offset:])))
		lon := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[offset+4:])))
		if math.IsNaN(lat) || math.IsNaN(lon) || math.IsInf(lat, 0) || math.IsInf(lon, 0) {
			continue
		}
		line = append(line, [2]float64{lat, lon})
	}
	return line, nil
}

// readClipTrack returns a clip's full track, packed or raw.
func readClipTrack(clip TelemetryClip) ([]TelemetryPoint, error) {
	if clip.Pack != nil {
		return readPackedTrack(clip.Pack)
	}
	return readTelemetryTrack(clip.ID, clip.PointWords)
}

// readClipLine returns a clip's map line: precomputed for packed days,
// simplified on the fly (and cached with the track) for raw ones.
func readClipLine(clip TelemetryClip) ([][2]float64, error) {
	if clip.Pack != nil {
		return readPackedLine(clip.Pack)
	}
	track, err := readTelemetryTrack(clip.ID, clip.PointWords)
	if err != nil {
		return nil, err
	}
	return simplifyLine(track), nil
}

const (
	lineToleranceMeters = 5.0
	lineMaxGapSeconds   = 30.0
)

// simplifyLine mirrors simplify_line in telemetry_index.py: Douglas-Peucker
// within about 5 m plus a point at least every 30 s.
func simplifyLine(track []TelemetryPoint) [][2]float64 {
	if len(track) <= 2 {
		line := make([][2]float64, 0, len(track))
		for _, p := range track {
			line = append(line, [2]float64{p.Lat, p.Lon})
		}
		return line
	}
	lat0 := track[0].Lat * math.Pi / 180
	xs := make([]float64, len(track))
	ys := make([]float64, len(track))
	for i, p := range track {
		xs[i] = p.Lon * 111320.0 * math.Cos(lat0)
		ys[i] = p.Lat * 110540.0
	}
	keep := make([]bool, len(track))
	keep[0], keep[len(track)-1] = true, true
	type span struct{ first, last int }
	stack := []span{{0, len(track) - 1}}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if s.last-s.first < 2 {
			continue
		}
		dx, dy := xs[s.last]-xs[s.first], ys[s.last]-ys[s.first]
		length := math.Hypot(dx, dy)
		worst, worstDistance := -1, 0.0
		for i := s.first + 1; i < s.last; i++ {
			var distance float64
			if length == 0 {
				distance = math.Hypot(xs[i]-xs[s.first], ys[i]-ys[s.first])
			} else {
				distance = math.Abs(dy*(xs[i]-xs[s.first])-dx*(ys[i]-ys[s.first])) / length
			}
			if distance > worstDistance {
				worst, worstDistance = i, distance
			}
		}
		if worstDistance > lineToleranceMeters {
			keep[worst] = true
			stack = append(stack, span{s.first, worst}, span{worst, s.last})
		}
	}
	lastKept := track[0].Seconds
	for i, p := range track {
		if keep[i] {
			lastKept = p.Seconds
		} else if p.Seconds-lastKept >= lineMaxGapSeconds {
			keep[i] = true
			lastKept = p.Seconds
		}
	}
	line := [][2]float64{}
	for i, p := range track {
		if keep[i] {
			line = append(line, [2]float64{p.Lat, p.Lon})
		}
	}
	return line
}

// normalizeRangeBound turns "YYYY-MM-DD", "YYYY-MM-DDTHH:MM" or a full
// "YYYY-MM-DDTHH:MM:SS" into the 19-character local form clips are stored
// with (recorded_at is the car's local time from the file name).
func normalizeRangeBound(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if len(value) >= len(layout) {
			if parsed, err := time.Parse(layout, value[:len(layout)]); err == nil {
				return parsed.Format("2006-01-02T15:04:05"), nil
			}
		}
	}
	return "", fmt.Errorf("invalid time %q: use YYYY-MM-DD or YYYY-MM-DDTHH:MM", value)
}

func recordedLocal(clip TelemetryClip) string {
	if len(clip.RecordedAt) >= 19 {
		return clip.RecordedAt[:19]
	}
	return clip.RecordedAt
}

// TelemetryDay summarises one day for the map timeline.
type TelemetryDay struct {
	Day            string      `json:"day"`
	Clips          int         `json:"clips"`
	DrivingSeconds float64     `json:"driving_seconds"`
	First          string      `json:"first"`
	Last           string      `json:"last"`
	HourMinutes    [24]float64 `json:"hour_minutes"`
	Saved          int         `json:"saved"`
	Recent         int         `json:"recent"`
}

type telemetryDaysResponse struct {
	Days          []TelemetryDay `json:"days"`
	TotalClips    int            `json:"total_clips"`
	TotalBytes    int64          `json:"total_bytes"`
	RetentionDays int            `json:"retention_days"`
}

func summarizeTelemetryDays(clips []TelemetryClip) []TelemetryDay {
	byDay := map[string]*TelemetryDay{}
	for _, clip := range clips {
		if clip.Samples == 0 || clip.State == "no-gps" {
			continue // parked minutes: no track
		}
		day := clipDay(clip)
		if !validDay(day) {
			continue
		}
		summary := byDay[day]
		if summary == nil {
			summary = &TelemetryDay{Day: day}
			byDay[day] = summary
		}
		local := recordedLocal(clip)
		summary.Clips++
		summary.DrivingSeconds += clip.Duration
		if summary.First == "" || local < summary.First {
			summary.First = local
		}
		if local > summary.Last {
			summary.Last = local
		}
		if len(local) >= 13 {
			if hour, err := time.Parse("15", local[11:13]); err == nil {
				summary.HourMinutes[hour.Hour()] += clip.Duration / 60
			}
		}
		if strings.HasPrefix(clip.ID, "SavedClips/") {
			summary.Saved++
		} else {
			summary.Recent++
		}
	}
	days := make([]TelemetryDay, 0, len(byDay))
	for _, summary := range byDay {
		days = append(days, *summary)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Day < days[j].Day })
	return days
}

func telemetryStoreBytes() int64 {
	var total int64
	_ = filepath.WalkDir(telemetryStoreDir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() {
			if info, err := entry.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// HandleTelemetryDays returns per-day driving totals for the map timeline and
// the size of the store for Settings. Reads day files only, never tracks.
// GET /api/telemetry/days
func HandleTelemetryDays(w http.ResponseWriter, r *http.Request) {
	settings := readTelemetrySettings()
	response := telemetryDaysResponse{Days: []TelemetryDay{}, RetentionDays: settings.RetentionDays}
	clips, err := readTelemetryDayFiles("", "")
	if err != nil {
		http.Error(w, `{"error":"could not read telemetry history"}`, http.StatusInternalServerError)
		return
	}
	response.Days = summarizeTelemetryDays(clips)
	for _, day := range response.Days {
		response.TotalClips += day.Clips
	}
	response.TotalBytes = telemetryStoreBytes()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// withTelemetryStoreLock holds the indexer's lock (the same flock as
// telemetry_index.py store_lock), waiting up to timeout for a running indexer.
func withTelemetryStoreLock(timeout time.Duration, fn func() error) error {
	if err := os.MkdirAll(telemetryStoreDir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(telemetryStoreDir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			return fmt.Errorf("telemetry indexer is busy: %w", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	return fn()
}
