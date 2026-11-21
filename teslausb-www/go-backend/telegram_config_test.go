package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramChatIDJSON_numeric(t *testing.T) {
	got := telegramChatIDJSON("-1001234567890")
	if string(got) != "-1001234567890" {
		t.Errorf("want numeric chat_id, got %s", got)
	}
}

func TestTelegramChatIDJSON_channel(t *testing.T) {
	got := telegramChatIDJSON("@mychannel")
	if string(got) != `"@mychannel"` {
		t.Errorf("want string chat_id, got %s", got)
	}
}

func TestTelegramChatIDJSON_escapesQuotes(t *testing.T) {
	raw := telegramChatIDJSON(`bad","injected":"1`)
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("chat_id did not marshal to a valid JSON string: %v (%s)", err, raw)
	}
	if s != `bad","injected":"1` {
		t.Errorf("round-trip mismatch, got %q", s)
	}
}

func TestTelegramTestMessagePayload_validJSON(t *testing.T) {
	for _, chatID := range []string{"123456", "@mychannel", `evil","x":"`} {
		payload, err := json.Marshal(telegramTestMessage{
			ChatID:              telegramChatIDJSON(chatID),
			Text:                "test",
			DisableNotification: true,
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var decoded map[string]interface{}
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatalf("payload for chatID %q is not valid JSON: %v\n%s", chatID, err, payload)
		}
		if !strings.Contains(string(payload), `"disable_notification":true`) {
			t.Errorf("payload missing disable_notification: %s", payload)
		}
	}
}

func TestTelegramConfigReportsServiceFailure(t *testing.T) {
	stubRemount(t)
	originalPath, originalRun := telegramEnvPath, systemctlRun
	t.Cleanup(func() { telegramEnvPath, systemctlRun = originalPath, originalRun })
	telegramEnvPath = t.TempDir() + "/telegram.env"
	systemctlRun = func(args ...string) error { return errors.New("unit unavailable") }
	req := httptest.NewRequest(http.MethodPost, "/api/config/telegram", strings.NewReader(`{"bot_token":"123456:test-token","chat_id":"123","enabled":true}`))
	response := httptest.NewRecorder()
	HandleTelegramConfig(response, req)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "service enable failed") {
		t.Errorf("unexpected body: %s", response.Body.String())
	}
}

func TestTelegramConfigRemountsRootAndWritesEnabledFlag(t *testing.T) {
	originalPath, originalRun, originalRemount, originalLock := telegramEnvPath, systemctlRun, remountRoot, rootFSLockPath
	t.Cleanup(func() {
		telegramEnvPath, systemctlRun, remountRoot, rootFSLockPath = originalPath, originalRun, originalRemount, originalLock
	})
	dir := t.TempDir()
	telegramEnvPath = dir + "/telegram.env"
	rootFSLockPath = dir + "/rootfs.lock"
	var events []string
	remountRoot = func(mode string) error {
		events = append(events, "remount:"+mode)
		return nil
	}
	systemctlRun = func(args ...string) error {
		if len(args) >= 2 && (args[0] == "enable" || args[0] == "restart") {
			events = append(events, "systemctl:"+args[0])
		}
		return nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/config/telegram", strings.NewReader(`{"bot_token":"123456:test-token-12345678","chat_id":"123","enabled":true}`))
	response := httptest.NewRecorder()
	HandleTelegramConfig(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	want := []string{"remount:rw", "systemctl:enable", "systemctl:restart", "remount:ro"}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("events=%v, want %v", events, want)
	}
}
