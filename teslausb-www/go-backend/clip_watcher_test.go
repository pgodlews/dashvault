package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// redirectClipWatcher overrides all configurable paths to a temp dir and
// returns a cleanup function.
func redirectClipWatcher(t *testing.T) (savedClips, triggerFile string) {
	t.Helper()
	tmp := t.TempDir()

	savedClips = filepath.Join(tmp, "SavedClips")
	sentry := filepath.Join(tmp, "SentryClips")
	track := filepath.Join(tmp, "TeslaTrackMode")
	triggerFile = filepath.Join(tmp, "archive_is_unreachable")

	for _, d := range []string{savedClips, sentry, track} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	orig := struct {
		dirs    []string
		trigger string
		deb     time.Duration
		retry   time.Duration
	}{clipWatchDirs, clipArchiveTrigger, clipWatchDebounce, clipWatchRetryDelay}

	clipWatchDirs = []string{savedClips, sentry, track}
	clipArchiveTrigger = triggerFile
	clipWatchDebounce = 50 * time.Millisecond   // fast for tests
	clipWatchRetryDelay = 50 * time.Millisecond // fast for tests

	t.Cleanup(func() {
		clipWatchDirs = orig.dirs
		clipArchiveTrigger = orig.trigger
		clipWatchDebounce = orig.deb
		clipWatchRetryDelay = orig.retry
	})
	return
}

func newTestWatcher(t *testing.T) *ClipWatcher {
	t.Helper()
	return NewClipWatcher(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

// waitForFile polls until path exists or deadline passes.
func waitForFile(t *testing.T, path string, deadline time.Duration) bool {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// ── tests ──────────────────────────────────────────────────────────────────────

func TestClipWatcher_NewDirTriggers(t *testing.T) {
	savedClips, trigger := redirectClipWatcher(t)
	cw := newTestWatcher(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go cw.Run(ctx)

	// Give the watcher time to arm.
	time.Sleep(100 * time.Millisecond)

	// Simulate car creating a new SavedClip event directory.
	clipDir := filepath.Join(savedClips, "2025-04-25_14-30-00")
	if err := os.Mkdir(clipDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if !waitForFile(t, trigger, 2*time.Second) {
		t.Error("trigger file was not created after a new clip directory appeared")
	}
}

func TestClipWatcher_FileCreateDoesNotTrigger(t *testing.T) {
	savedClips, trigger := redirectClipWatcher(t)
	cw := newTestWatcher(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go cw.Run(ctx)

	time.Sleep(100 * time.Millisecond)

	// Create a plain file (not a directory) inside SavedClips.
	if err := os.WriteFile(filepath.Join(savedClips, "event.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Trigger must NOT fire within debounce + margin.
	time.Sleep(clipWatchDebounce + 100*time.Millisecond)
	if _, err := os.Stat(trigger); err == nil {
		t.Error("trigger file must not be created for a plain file event")
	}
}

func TestClipWatcher_MultipleDirectoriesDebounceToOneTrigger(t *testing.T) {
	savedClips, trigger := redirectClipWatcher(t)
	cw := newTestWatcher(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go cw.Run(ctx)

	time.Sleep(100 * time.Millisecond)

	// Simulate 3 clip dirs created in rapid succession (front/back/left/right
	// cameras all save at once).
	for i := range 3 {
		dir := filepath.Join(savedClips, fmt.Sprintf("2025-04-25_14-3%d-00", i))
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	if !waitForFile(t, trigger, 2*time.Second) {
		t.Fatal("trigger file was not created")
	}

	// Remove the trigger and verify it is NOT re-created a second time.
	os.Remove(trigger)
	time.Sleep(clipWatchDebounce + 100*time.Millisecond)
	if _, err := os.Stat(trigger); err == nil {
		t.Error("trigger fired more than once for a burst of directories")
	}
}

func TestClipWatcher_RetriesWhenDirsAbsent(t *testing.T) {
	tmp := t.TempDir()
	triggerFile := filepath.Join(tmp, "archive_is_unreachable")
	savedClips := filepath.Join(tmp, "SavedClips")

	origDirs := clipWatchDirs
	origTrigger := clipArchiveTrigger
	origDeb := clipWatchDebounce
	origRetry := clipWatchRetryDelay
	t.Cleanup(func() {
		clipWatchDirs = origDirs
		clipArchiveTrigger = origTrigger
		clipWatchDebounce = origDeb
		clipWatchRetryDelay = origRetry
	})

	clipWatchDirs = []string{savedClips}
	clipArchiveTrigger = triggerFile
	clipWatchDebounce = 50 * time.Millisecond
	clipWatchRetryDelay = 50 * time.Millisecond

	cw := newTestWatcher(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go cw.Run(ctx)

	// Watcher is running but savedClips doesn't exist yet — no watches armed.
	time.Sleep(150 * time.Millisecond)

	// Now create the directory — the watcher should pick it up on next retry.
	if err := os.MkdirAll(savedClips, 0o755); err != nil {
		t.Fatal(err)
	}
	// Wait for retry loop to re-arm (2× retry delay is plenty).
	time.Sleep(200 * time.Millisecond)

	// Create a clip directory — trigger should fire.
	if err := os.Mkdir(filepath.Join(savedClips, "2025-04-25_15-00-00"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !waitForFile(t, triggerFile, 2*time.Second) {
		t.Error("trigger did not fire after watched dir became available")
	}
}

func TestClipWatcher_CancelStops(t *testing.T) {
	redirectClipWatcher(t)
	cw := newTestWatcher(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		cw.Run(ctx)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("Run did not return after context cancellation")
	}
}
