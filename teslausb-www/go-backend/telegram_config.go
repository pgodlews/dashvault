package main

// telegram_config.go — read/write Telegram bot configuration.
//
// Config lives in /mutable/teslausb_telegram.env (always writable — no
// remount needed). Format: bare KEY=value lines (no "export" prefix).
//
// API surface:
//   GET  /api/config/telegram       — status + masked token + chat ID + service state
//   POST /api/config/telegram       — write config, enable/disable service
//   POST /api/config/telegram/test  — send a real test message via Telegram API
//   POST /api/config/telegram/find-chats — list chats that recently messaged the bot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ── path constants (vars so tests can redirect) ────────────────────────────────

var telegramEnvPath = "/mutable/teslausb_telegram.env"
var systemctlRun = func(args ...string) error {
	return exec.Command("systemctl", args...).Run()
}

const telegramServiceName = "teslausb-telegram-bot.service"

var (
	telegramTokenRe  = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)
	telegramChatIDRe = regexp.MustCompile(`^[-@A-Za-z0-9_, ]*$`)
)

// ── types ──────────────────────────────────────────────────────────────────────

// TelegramConfig is the GET response. The raw token is never sent to the client.
type TelegramConfig struct {
	HasToken       bool   `json:"has_token"`    // true when a token is stored on disk
	MaskedToken    string `json:"masked_token"` // "••••<last8chars>" or ""
	ChatID         string `json:"chat_id"`
	SilentNotify   bool   `json:"silent_notify"`
	ServiceEnabled bool   `json:"service_enabled"` // systemctl is-enabled
	ServiceRunning bool   `json:"service_running"` // systemctl is-active
}

// telegramWriteRequest is the POST body.
type telegramWriteRequest struct {
	BotToken     string `json:"bot_token"` // empty + keep_token=true → keep existing
	KeepToken    bool   `json:"keep_token"`
	ChatID       string `json:"chat_id"`
	SilentNotify bool   `json:"silent_notify"`
	Enabled      bool   `json:"enabled"`
}

// ── env file parser ────────────────────────────────────────────────────────────

// readTelegramEnv parses /mutable/teslausb_telegram.env.
// Returns zero values (no error) when the file does not exist yet.
func readTelegramEnv() (token, chatID string, silent bool, err error) {
	data, err := os.ReadFile(telegramEnvPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		// Strip surrounding single or double quotes.
		if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		switch k {
		case "TELEGRAM_BOT_TOKEN":
			token = v
		case "TELEGRAM_CHAT_ID":
			chatID = v
		case "TELEGRAM_SILENT_NOTIFY":
			silent = strings.EqualFold(v, "true")
		}
	}
	return token, chatID, silent, nil
}

// writeTelegramEnv writes the env file atomically.
func writeTelegramEnv(token, chatID string, silent, enabled bool) error {
	silentStr := "false"
	if silent {
		silentStr = "true"
	}
	enabledStr := "false"
	if enabled {
		enabledStr = "true"
	}
	content := fmt.Sprintf(
		"TELEGRAM_ENABLED=%s\nTELEGRAM_BOT_TOKEN=%s\nTELEGRAM_CHAT_ID=%s\nTELEGRAM_SILENT_NOTIFY=%s\n",
		enabledStr, token, chatID, silentStr,
	)
	return writeFileAtomic(telegramEnvPath, []byte(content), 0o600)
}

// ── systemd helpers ────────────────────────────────────────────────────────────

// systemctlIs returns true if `systemctl <verb> --quiet <unit>` exits 0.
func systemctlIs(verb, unit string) bool {
	return systemctlRun(verb, "--quiet", unit) == nil
}

// ── config reader ──────────────────────────────────────────────────────────────

func readTelegramConfig() (TelegramConfig, error) {
	token, chatID, silent, err := readTelegramEnv()
	if err != nil {
		return TelegramConfig{}, err
	}

	masked := ""
	hasToken := token != ""
	if hasToken {
		if len(token) >= 8 {
			masked = "••••" + token[len(token)-8:]
		} else {
			masked = "••••"
		}
	}

	return TelegramConfig{
		HasToken:       hasToken,
		MaskedToken:    masked,
		ChatID:         chatID,
		SilentNotify:   silent,
		ServiceEnabled: systemctlIs("is-enabled", telegramServiceName),
		ServiceRunning: systemctlIs("is-active", telegramServiceName),
	}, nil
}

// ── HTTP handlers ──────────────────────────────────────────────────────────────

// HandleTelegramConfig — GET /api/config/telegram, POST /api/config/telegram
func HandleTelegramConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodGet {
		cfg, err := readTelegramConfig()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(cfg)
		return
	}

	// POST — decode request
	var req telegramWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Both values go into a line-based env file, and the token into a URL.
	req.BotToken = strings.TrimSpace(req.BotToken)
	req.ChatID = strings.TrimSpace(req.ChatID)
	if req.BotToken != "" && !telegramTokenRe.MatchString(req.BotToken) {
		writeJSONError(w, http.StatusBadRequest, "token must look like 123456789:AA… as given by BotFather, without a \"bot\" prefix")
		return
	}
	if !telegramChatIDRe.MatchString(req.ChatID) {
		writeJSONError(w, http.StatusBadRequest, "chat_id must be one or more numeric IDs or @channel names, separated by commas")
		return
	}

	// Resolve the token to write.
	tokenToWrite := req.BotToken
	if req.KeepToken || tokenToWrite == "" {
		existing, _, _, err := readTelegramEnv()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "could not read existing token: "+err.Error())
			return
		}
		tokenToWrite = existing
	}

	// Reject enabling with no token.
	if req.Enabled && tokenToWrite == "" {
		writeJSONError(w, http.StatusBadRequest, "cannot enable bot: no token configured")
		return
	}

	// Write the env file.
	if err := writeTelegramEnv(tokenToWrite, req.ChatID, req.SilentNotify, req.Enabled); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "write failed: "+err.Error())
		return
	}

	// Enable/disable + restart/stop service. systemctl enable/disable mutates
	// symlinks under /etc/systemd/system on the read-only root partition.
	actions := []string{"stop", "disable"}
	if req.Enabled {
		actions = []string{"enable", "restart"}
	}
	var failedAction string
	if err := withConfRW(func() error {
		for _, action := range actions {
			if err := systemctlRun(action, telegramServiceName); err != nil {
				failedAction = action
				return err
			}
		}
		return nil
	}); err != nil {
		if failedAction != "" {
			writeJSONError(w, http.StatusInternalServerError, "service "+failedAction+" failed")
		} else {
			writeJSONError(w, http.StatusInternalServerError, "service update failed: "+err.Error())
		}
		return
	}

	cfg, err := readTelegramConfig()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(cfg)
}

// telegramTestMessage is the JSON body sent to Telegram's sendMessage API.
// ChatID is a RawMessage because Telegram accepts either a numeric ID or a
// string like "@mychannel".
type telegramTestMessage struct {
	ChatID              json.RawMessage `json:"chat_id"`
	Text                string          `json:"text"`
	DisableNotification bool            `json:"disable_notification"`
}

// telegramChatIDJSON renders a chat ID as JSON: numeric IDs stay numbers,
// anything else (e.g. "@mychannel") becomes a properly escaped JSON string.
func telegramChatIDJSON(chatID string) json.RawMessage {
	if n, err := strconv.ParseInt(chatID, 10, 64); err == nil {
		b, _ := json.Marshal(n)
		return b
	}
	b, _ := json.Marshal(chatID)
	return b
}

// requestErrorReason describes a failed HTTP request without its URL. The
// text of a *url.Error includes the URL, and Telegram's carries the bot token.
func requestErrorReason(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err.Error()
	}
	return err.Error()
}

// HandleTelegramTest — POST /api/config/telegram/test
// Reads token from disk and sends a real Telegram message.
func HandleTelegramTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	token, chatID, silent, err := readTelegramEnv()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if token == "" || chatID == "" {
		writeJSONError(w, http.StatusBadRequest, "bot not configured — save token and chat ID first")
		return
	}

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	payload, err := json.Marshal(telegramTestMessage{
		ChatID:              telegramChatIDJSON(chatID),
		Text:                "✅ Dashvault test message — bot is configured correctly.",
		DisableNotification: silent,
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(apiURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "Telegram API unreachable: "+requestErrorReason(err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("Telegram API returned %d — check your token", resp.StatusCode))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"result": "ok"})
}
