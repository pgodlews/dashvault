package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A cancelled request context must abort the snapshot command instead of
// letting the child run to completion.
func TestSnapshotCmd_cancelledContext(t *testing.T) {
	f := filepath.Join(t.TempDir(), "x.log")
	if err := os.WriteFile(f, []byte("line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := logSource{files: []string{f}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, err := src.snapshotCmd(ctx, 10).Output(); err == nil {
		t.Errorf("expected error from cancelled context, got output: %q", out)
	}
}

func TestSnapshotCmd_runsWithLiveContext(t *testing.T) {
	f := filepath.Join(t.TempDir(), "x.log")
	if err := os.WriteFile(f, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := logSource{files: []string{f}}

	out, err := src.snapshotCmd(context.Background(), 10).Output()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "hello\n" {
		t.Errorf("want %q, got %q", "hello\n", out)
	}
}
