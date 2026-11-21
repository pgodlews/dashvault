package main

// clip_watcher.go — inotify-based watcher for new TeslaCam clip events.
//
// Watches /mnt/cam/SavedClips, /mnt/cam/SentryClips, and
// /mnt/cam/TeslaTrackMode for new directories (each new directory = a new
// clip event written by the car).  When one appears, touches
// /tmp/archive_is_unreachable after a short debounce to trigger an immediate
// archive cycle in archiveloop — rather than waiting up to SNAPSHOT_INTERVAL
// seconds for the next scheduled snapshot.
//
// RecentClips is intentionally excluded: the car writes there continuously
// during normal recording, which would fire the trigger every few seconds.
//
// Resilience: the camera disk is mounted/unmounted during archive cycles.
// The watcher detects when watched directories disappear and retries until
// they reappear, so it automatically re-arms after every archive cycle.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ── configurable paths (vars so tests can redirect) ────────────────────────────

var (
	clipWatchDirs = []string{
		"/mnt/cam/SavedClips",
		"/mnt/cam/SentryClips",
		"/mnt/cam/TeslaTrackMode",
	}
	clipArchiveTrigger  = "/tmp/archive_is_unreachable"
	clipWatchDebounce   = 10 * time.Second
	clipWatchRetryDelay = 30 * time.Second
)

// ── ClipWatcher ────────────────────────────────────────────────────────────────

// ClipWatcher watches Tesla clip directories and fires an archive trigger when
// new clip directories appear.
type ClipWatcher struct {
	logger      *slog.Logger
	watchDirs   []string
	triggerFile string
	debounce    time.Duration
	retryDelay  time.Duration

	mu    sync.Mutex
	timer *time.Timer
}

// NewClipWatcher creates a ClipWatcher.  Call Run to start it.
// Configuration is captured from package-level vars at construction time so
// that goroutines and timers always read struct fields (never package globals),
// keeping the race detector happy in tests that redirect those globals.
func NewClipWatcher(logger *slog.Logger) *ClipWatcher {
	return &ClipWatcher{
		logger:      logger,
		watchDirs:   clipWatchDirs,
		triggerFile: clipArchiveTrigger,
		debounce:    clipWatchDebounce,
		retryDelay:  clipWatchRetryDelay,
	}
}

// Run blocks until ctx is cancelled, watching for new clip directories and
// triggering archive cycles with debounce.  It retries silently when the
// camera disk is not yet mounted.
func (cw *ClipWatcher) Run(ctx context.Context) {
	for {
		if err := cw.watch(ctx); err != nil && ctx.Err() == nil {
			cw.logger.Debug("clip watcher: retrying", "err", err, "delay", cw.retryDelay)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(cw.retryDelay):
		}
	}
}

// watch creates an fsnotify watcher, adds all available clip directories, and
// processes events until context cancellation or a watch error.  Returns a
// non-nil error when the watcher should be restarted.
func (cw *ClipWatcher) watch(ctx context.Context) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fsnotify.NewWatcher: %w", err)
	}
	defer w.Close()

	added := 0
	for _, dir := range cw.watchDirs {
		if err := w.Add(dir); err == nil {
			cw.logger.Info("clip watcher: watching", "dir", dir)
			added++
		}
		// Silently skip dirs that don't exist yet (disk not mounted).
	}
	if added == 0 {
		return fmt.Errorf("no clip directories available yet")
	}

	for {
		select {
		case <-ctx.Done():
			return nil

		case event, ok := <-w.Events:
			if !ok {
				return fmt.Errorf("event channel closed")
			}
			cw.handleEvent(event)

		case err, ok := <-w.Errors:
			if !ok {
				return fmt.Errorf("error channel closed")
			}
			// A watched dir was removed (disk unmounted during archive cycle).
			// Return so the outer loop re-establishes watches once it remounts.
			return fmt.Errorf("watcher error: %w", err)
		}
	}
}

// handleEvent filters inotify events and schedules a debounced archive trigger
// when a new directory is created (= new clip event from the car).
func (cw *ClipWatcher) handleEvent(event fsnotify.Event) {
	if !event.Has(fsnotify.Create) {
		return
	}
	// Ignore plain files — only directory creation signals a new clip event.
	info, err := os.Stat(event.Name)
	if err != nil || !info.IsDir() {
		return
	}

	cw.logger.Info("clip watcher: new clip directory", "path", event.Name)
	cw.scheduleTrigger()
}

// scheduleTrigger resets the debounce timer.  Multiple clip directories created
// in quick succession (e.g. front/back/left/right cameras for one event) coalesce
// into a single archive trigger.
func (cw *ClipWatcher) scheduleTrigger() {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	if cw.timer != nil {
		cw.timer.Stop()
	}
	cw.timer = time.AfterFunc(cw.debounce, cw.fireTrigger)
}

// fireTrigger touches the archive sentinel file, causing archiveloop's
// wait_for_archive_to_be_unreachable to break early and start a new archive
// cycle immediately.
func (cw *ClipWatcher) fireTrigger() {
	f, err := os.Create(cw.triggerFile)
	if err != nil {
		cw.logger.Warn("clip watcher: failed to touch trigger", "file", cw.triggerFile, "err", err)
		return
	}
	f.Close()
	cw.logger.Info("clip watcher: archive trigger fired", "trigger", cw.triggerFile)
}
