package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func useSetupConf(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "teslausb_setup_variables.conf")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	original := setupConfFile
	setupConfFile = path
	t.Cleanup(func() { setupConfFile = original })
	return path
}

func TestIsConfiguredAcceptsArchiveServerNames(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		want    bool
	}{
		{name: "legacy server", content: "export ARCHIVE_SERVER=nas\n", want: true},
		{name: "host", content: "ARCHIVE_HOST=nas\n", want: true},
		{name: "missing", content: "export SHARE_NAME=tesla\n", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			useSetupConf(t, test.content)
			if got := isConfigured(); got != test.want {
				t.Fatalf("isConfigured() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestWriteSetupConfUsesRuntimeVariableNames(t *testing.T) {
	path := useSetupConf(t, "")
	stubRemount(t)
	req := SetupApplyRequest{
		ArchiveType: "cifs",
		ArchiveHost: "nas",
		ShareName:   "tesla/dashcam",
		Username:    "tesla",
		Password:    "secret",
	}
	if err := writeSetupConf(req); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, expected := range []string{
		"export ARCHIVE_SERVER='nas'",
		"export SHARE_NAME='tesla/dashcam'",
		"export SHARE_USER='tesla'",
		"export SHARE_PASSWORD='secret'",
	} {
		if !strings.Contains(content, expected) {
			t.Errorf("configuration missing %q", expected)
		}
	}
}

func TestIsConfiguredAcceptsCompletedInstallWithoutArchive(t *testing.T) {
	useSetupConf(t, "export ARCHIVE_SYSTEM=none\n")
	marker := filepath.Join(t.TempDir(), "TESLAUSB_SETUP_FINISHED")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	original := setupFinishedFile
	setupFinishedFile = marker
	t.Cleanup(func() { setupFinishedFile = original })
	if !isConfigured() {
		t.Fatal("completed no-archive installation reported as unconfigured")
	}
}

func TestWriteSetupConfPreservesUnrelatedSettings(t *testing.T) {
	path := useSetupConf(t, "export SNAPSHOT_INTERVAL=300\nexport TELEMETRY_MAP_ENABLED=true\n")
	stubRemount(t)
	req := SetupApplyRequest{ArchiveType: "none"}
	if err := writeSetupConf(req); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"export SNAPSHOT_INTERVAL=300",
		"export TELEMETRY_MAP_ENABLED=true",
		"export ARCHIVE_SYSTEM='none'",
	} {
		if !strings.Contains(string(data), expected) {
			t.Errorf("configuration missing %q", expected)
		}
	}
}

func TestSetupApplyUsesSupportedEntryPointAndMarksSuccess(t *testing.T) {
	if filepath.Base(setupConfigureScript) != "setup-teslausb" {
		t.Fatalf("setup entry point = %q, want setup-teslausb", setupConfigureScript)
	}
	useSetupConf(t, "")
	stubRemount(t)
	directory := t.TempDir()
	script := filepath.Join(directory, "setup-teslausb")
	if err := os.WriteFile(script, []byte("#!/bin/bash\necho configured\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(directory, "provisioning_done")
	originalScript, originalSentinel := setupConfigureScript, setupSentinelFile
	setupConfigureScript, setupSentinelFile = script, sentinel
	t.Cleanup(func() {
		setupConfigureScript, setupSentinelFile = originalScript, originalSentinel
	})

	request := httptest.NewRequest(http.MethodPost, "/api/setup/apply", strings.NewReader(`{"archive_type":"none"}`))
	response := httptest.NewRecorder()
	HandleSetupApply(response, request)
	if !strings.Contains(response.Body.String(), `"done":true`) {
		t.Fatalf("setup response = %s", response.Body.String())
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("success did not write completion marker: %v", err)
	}
}

func TestSetupApplySurvivesCancelledRequest(t *testing.T) {
	useSetupConf(t, "")
	stubRemount(t)
	directory := t.TempDir()
	marker := filepath.Join(directory, "ran")
	script := filepath.Join(directory, "setup-teslausb")
	body := "#!/bin/bash\nprintf ran > " + shellQuote(marker) + "\nsleep 0.2\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(directory, "provisioning_done")
	originalScript, originalSentinel := setupConfigureScript, setupSentinelFile
	setupConfigureScript, setupSentinelFile = script, sentinel
	t.Cleanup(func() {
		setupConfigureScript, setupSentinelFile = originalScript, originalSentinel
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/api/setup/apply", strings.NewReader(`{"archive_type":"none"}`)).WithContext(ctx)
	response := httptest.NewRecorder()
	HandleSetupApply(response, request)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("cancelled request killed setup: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("setup did not record completion: %v", err)
	}
}

func TestSetupApplyFailureDoesNotMarkProvisioningComplete(t *testing.T) {
	useSetupConf(t, "")
	stubRemount(t)
	directory := t.TempDir()
	script := filepath.Join(directory, "setup-teslausb")
	if err := os.WriteFile(script, []byte("#!/bin/bash\necho failed\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(directory, "provisioning_done")
	originalScript, originalSentinel := setupConfigureScript, setupSentinelFile
	setupConfigureScript, setupSentinelFile = script, sentinel
	t.Cleanup(func() {
		setupConfigureScript, setupSentinelFile = originalScript, originalSentinel
	})

	request := httptest.NewRequest(http.MethodPost, "/api/setup/apply", strings.NewReader(`{"archive_type":"none"}`))
	response := httptest.NewRecorder()
	HandleSetupApply(response, request)
	if !strings.Contains(response.Body.String(), "setup-teslausb") {
		t.Fatalf("failure response = %s", response.Body.String())
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("failed setup wrote completion marker")
	}
}

func TestProvisionedConfigWithoutArchiveCountsAsConfigured(t *testing.T) {
	originalFinished := setupFinishedFile
	setupFinishedFile = filepath.Join(t.TempDir(), "not-finished")
	t.Cleanup(func() { setupFinishedFile = originalFinished })

	useSetupConf(t, "export ARCHIVE_SYSTEM='none'\nexport WEBUI_USERNAME='owner'\n")
	if !isConfigured() {
		t.Fatal("a no-archive config should not send the web app back to the wizard")
	}
	useSetupConf(t, "export SSID='home'\n")
	if isConfigured() {
		t.Fatal("a config with no archive choice is not configured")
	}
}

func TestSetupApplyRefusedOnceConfigured(t *testing.T) {
	originalFinished := setupFinishedFile
	setupFinishedFile = filepath.Join(t.TempDir(), "not-finished")
	t.Cleanup(func() { setupFinishedFile = originalFinished })

	before := "export ARCHIVE_SYSTEM='none'\n"
	path := useSetupConf(t, before)
	request := httptest.NewRequest(http.MethodPost, "/api/setup/apply",
		strings.NewReader(`{"archive_type":"cifs","archive_host":"nas","share_name":"cam"}`))
	response := httptest.NewRecorder()
	HandleSetupApply(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if data, _ := os.ReadFile(path); string(data) != before {
		t.Fatalf("config changed: %q", data)
	}
}
