package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestArchiveClipDefaultsAndExistingValues(t *testing.T) {
	original := confFilePath
	t.Cleanup(func() { confFilePath = original })
	confFilePath = t.TempDir() + "/missing.conf"
	cfg, err := readArchiveConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SavedClips || !cfg.SentryClips || cfg.RecentClips {
		t.Errorf("unexpected defaults: %+v", cfg)
	}

	content := "export ARCHIVE_SAVEDCLIPS=false\nexport ARCHIVE_SENTRYCLIPS=true\nexport ARCHIVE_RECENTCLIPS=true\n"
	if err := os.WriteFile(confFilePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = readArchiveConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SavedClips || !cfg.SentryClips || !cfg.RecentClips {
		t.Errorf("existing values not preserved: %+v", cfg)
	}
}

func TestWriteArchiveClipSettings(t *testing.T) {
	stubArchiveEnvironment(t)
	original := confFilePath
	t.Cleanup(func() { confFilePath = original })
	confFilePath = t.TempDir() + "/config.conf"
	if err := os.WriteFile(confFilePath, []byte("export ARCHIVE_SYSTEM=none\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stubRemount(t)
	expected := ArchiveConfig{System: "none", SavedClips: false, SentryClips: true, RecentClips: true}
	if err := writeArchiveConfig(expected); err != nil {
		t.Fatal(err)
	}
	actual, err := readArchiveConfig()
	if err != nil {
		t.Fatal(err)
	}
	if actual.SavedClips != expected.SavedClips || actual.SentryClips != expected.SentryClips || actual.RecentClips != expected.RecentClips {
		t.Errorf("clip settings = %+v, want %+v", actual, expected)
	}
}

func TestRecentClipCameras(t *testing.T) {
	stubArchiveEnvironment(t)
	original := confFilePath
	t.Cleanup(func() { confFilePath = original })
	confFilePath = t.TempDir() + "/config.conf"
	if err := os.WriteFile(confFilePath, []byte("export ARCHIVE_SYSTEM=none\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stubRemount(t)

	cfg, err := readArchiveConfig()
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(cfg.RecentCameras) != fmt.Sprint(RecentCameraNames) {
		t.Fatalf("unset setting should mean every camera, got %v", cfg.RecentCameras)
	}

	cfg.RecentClips = true
	cfg.RecentCameras = []string{"left_repeater", "back", "back"}
	if err := writeArchiveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(confFilePath)
	if got := readConfKey(string(data), "RECENTCLIPS_CAMERAS"); got != "back left_repeater" {
		t.Errorf("conf value = %q, want display order without duplicates", got)
	}

	// A client that does not send the field leaves the selection alone.
	cfg.RecentCameras = nil
	if err := writeArchiveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	cfg, _ = readArchiveConfig()
	if fmt.Sprint(cfg.RecentCameras) != "[back left_repeater]" {
		t.Errorf("selection lost when field omitted: %v", cfg.RecentCameras)
	}

	// Selecting every camera clears the key, so it means "all" again.
	cfg.RecentCameras = append([]string{}, RecentCameraNames...)
	if err := writeArchiveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(confFilePath)
	if got := readConfKey(string(data), "RECENTCLIPS_CAMERAS"); got != "" {
		t.Errorf("all cameras should be stored as empty, got %q", got)
	}

	if cfg.RecentPruneSnapshots == nil || *cfg.RecentPruneSnapshots {
		t.Fatalf("snapshot pruning must default to off: %v", cfg.RecentPruneSnapshots)
	}
	on := true
	cfg.RecentPruneSnapshots = &on
	if err := writeArchiveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.RecentPruneSnapshots = nil // older UI build: keep the setting
	if err := writeArchiveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = readArchiveConfig(); cfg.RecentPruneSnapshots == nil || !*cfg.RecentPruneSnapshots {
		t.Errorf("snapshot pruning setting lost: %v", cfg.RecentPruneSnapshots)
	}

	for _, bad := range [][]string{{}, {"front", "roof"}} {
		if _, err := recentCamerasConfValue(bad); err == nil {
			t.Errorf("%v should be rejected", bad)
		}
	}
	if got := parseRecentCameras("front,back"); fmt.Sprint(got) != "[front back]" {
		t.Errorf("comma separated value parsed as %v", got)
	}
}

func TestUpsertConfKeyPreservesLiteralShellValues(t *testing.T) {
	content := "export SHARE_PASSWORD='old'\n"
	for _, value := range []string{"pa$word's", "$1$HOME$%{x}"} {
		content = upsertConfKey(content, "SHARE_PASSWORD", value)
		if got := readConfKey(content, "SHARE_PASSWORD"); got != value {
			t.Fatalf("read-back = %q, want %q; content=%q", got, value, content)
		}
	}
}

func TestConcurrentConfigurationUpdatesPreserveEveryKey(t *testing.T) {
	original := confFilePath
	t.Cleanup(func() { confFilePath = original })
	confFilePath = t.TempDir() + "/config.conf"
	if err := os.WriteFile(confFilePath, []byte("export ARCHIVE_SYSTEM=none\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stubRemount(t)

	const writers = 12
	start := make(chan struct{})
	var wait sync.WaitGroup
	errors := make(chan error, writers)
	for index := range writers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			key := fmt.Sprintf("TEST_KEY_%d", index)
			value := fmt.Sprintf("value$%d's", index)
			errors <- withConfRW(func() error {
				data, err := os.ReadFile(confFilePath)
				if err != nil {
					return err
				}
				content := upsertConfKey(string(data), key, value)
				return writeFileAtomic(confFilePath, []byte(content), 0o600)
			})
		}()
	}
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(confFilePath)
	if err != nil {
		t.Fatal(err)
	}
	for index := range writers {
		key := fmt.Sprintf("TEST_KEY_%d", index)
		want := fmt.Sprintf("value$%d's", index)
		if got := readConfKey(string(data), key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestArchiveConfigNFSRoundTripAndCIFSCredentialsUpdate(t *testing.T) {
	stubArchiveEnvironment(t)
	originalConf := confFilePath
	originalCreds := cifsCredentialsFilePath
	t.Cleanup(func() {
		confFilePath = originalConf
		cifsCredentialsFilePath = originalCreds
	})
	dir := t.TempDir()
	confFilePath = dir + "/config.conf"
	cifsCredentialsFilePath = dir + "/.teslaCamArchiveCredentials"
	stubRemount(t)

	// Legacy NFS config with ARCHIVE_SERVER and SHARE_NAME should populate NFS struct on read.
	legacyNFS := "export ARCHIVE_SYSTEM='nfs'\nexport ARCHIVE_SERVER='nas.local'\nexport SHARE_NAME='/volume1/tesla'\n"
	if err := os.WriteFile(confFilePath, []byte(legacyNFS), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := readArchiveConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NFS.Server != "nas.local" || cfg.NFS.Path != "/volume1/tesla" {
		t.Fatalf("NFS fallback read failed: %+v", cfg.NFS)
	}

	// Writing CIFS config should also update cifsCredentialsFilePath.
	cifsCfg := ArchiveConfig{
		System: "cifs",
		CIFS: CIFSBackendConfig{
			Server:   "smb.local",
			Share:    "TeslaCam",
			User:     "pi",
			Password: "secret",
			Domain:   "WORKGROUP",
		},
	}
	if err := writeArchiveConfig(cifsCfg); err != nil {
		t.Fatal(err)
	}
	credsData, err := os.ReadFile(cifsCredentialsFilePath)
	if err != nil {
		t.Fatal(err)
	}
	wantCreds := "username=pi\npassword=secret\ndomain=WORKGROUP\n"
	if string(credsData) != wantCreds {
		t.Errorf("CIFS credentials = %q, want %q", string(credsData), wantCreds)
	}
}

func TestDetectRecentCameras(t *testing.T) {
	root := t.TempDir()
	if got := detectRecentCameras(root); len(got) != 0 {
		t.Fatalf("empty TeslaCam should detect nothing, got %v", got)
	}
	day := filepath.Join(root, "RecentClips", "2026-11-11")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, camera := range []string{"right_repeater", "front", "back", "left_repeater"} {
		// Symlinks to snapshots that are not mounted must still count.
		if err := os.Symlink("/nonexistent/snap", filepath.Join(day, "2026-11-11_10-00-00-"+camera+".mp4")); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(day, "thumb.png"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(detectRecentCameras(root)); got != "[front back left_repeater right_repeater]" {
		t.Errorf("HW3 car detected as %s", got)
	}
	event := filepath.Join(root, "SavedClips", "2026-11-11_10-05-00")
	if err := os.MkdirAll(event, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(event, "2026-11-11_10-04-00-left_pillar.mp4"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectRecentCameras(root); len(got) != 5 || got[4] != "left_pillar" {
		t.Errorf("pillar camera in a saved event not detected: %v", got)
	}
}
