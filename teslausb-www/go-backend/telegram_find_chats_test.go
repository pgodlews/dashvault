package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	savedTelegramToken  = "111111:saved-token-value"
	pastedTelegramToken = "222222:pasted-token-value"
)

type findChatsFixture struct {
	requests     []string
	systemctl    []string
	serviceAlive bool
	reply        func(w http.ResponseWriter)
}

func newFindChatsFixture(t *testing.T, savedToken string) *findChatsFixture {
	t.Helper()
	f := &findChatsFixture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.URL.RequestURI())
		if f.reply != nil {
			f.reply(w)
			return
		}
		fmt.Fprint(w, `{"ok":true,"result":[]}`)
	}))
	t.Cleanup(srv.Close)

	originalBase, originalRecent, originalEnv, originalRun := telegramAPIBase, telegramRecentChatsPath, telegramEnvPath, systemctlRun
	t.Cleanup(func() {
		telegramAPIBase, telegramRecentChatsPath, telegramEnvPath, systemctlRun = originalBase, originalRecent, originalEnv, originalRun
	})
	dir := t.TempDir()
	telegramAPIBase = srv.URL
	telegramRecentChatsPath = filepath.Join(dir, "recent.json")
	telegramEnvPath = filepath.Join(dir, "telegram.env")
	if savedToken != "" {
		content := "TELEGRAM_ENABLED=true\nTELEGRAM_BOT_TOKEN=" + savedToken + "\nTELEGRAM_CHAT_ID=999\n"
		if err := os.WriteFile(telegramEnvPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	systemctlRun = func(args ...string) error {
		f.systemctl = append(f.systemctl, args[0])
		if args[0] == "is-active" && f.serviceAlive {
			return nil
		}
		return fmt.Errorf("inactive")
	}
	return f
}

func findChats(t *testing.T, body string) (*httptest.ResponseRecorder, []telegramFoundChat) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/config/telegram/find-chats", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleTelegramFindChats(w, req)
	var decoded struct {
		Chats []telegramFoundChat `json:"chats"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &decoded)
	return w, decoded.Chats
}

func TestFindChats_doesNotAcknowledgeUpdates(t *testing.T) {
	f := newFindChatsFixture(t, savedTelegramToken)
	if w, _ := findChats(t, `{}`); w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if len(f.requests) != 1 {
		t.Fatalf("requests=%v", f.requests)
	}
	got := f.requests[0]
	if !strings.HasPrefix(got, "/bot"+savedTelegramToken+"/getUpdates?") {
		t.Errorf("unexpected request %s", got)
	}
	for _, forbidden := range []string{"offset", "allowed_updates"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("request must not send %s: %s", forbidden, got)
		}
	}
}

func TestFindChats_usesPastedToken(t *testing.T) {
	f := newFindChatsFixture(t, savedTelegramToken)
	f.serviceAlive = true
	if w, _ := findChats(t, `{"bot_token":" `+pastedTelegramToken+` "}`); w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if len(f.requests) != 1 || !strings.Contains(f.requests[0], pastedTelegramToken) {
		t.Fatalf("requests=%v", f.requests)
	}
	data, err := os.ReadFile(telegramEnvPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), pastedTelegramToken) {
		t.Error("looking up chats must not save the token")
	}
}

func TestFindChats_listsNewestFirstWithoutDuplicates(t *testing.T) {
	f := newFindChatsFixture(t, savedTelegramToken)
	f.reply = func(w http.ResponseWriter) {
		fmt.Fprint(w, `{"ok":true,"result":[
			{"update_id":1,"message":{"date":100,"chat":{"id":5,"type":"private","first_name":"Ann","last_name":"Lee","username":"ann"}}},
			{"update_id":2,"message":{"date":200,"chat":{"id":-1007,"type":"supergroup","title":"Garage"}}},
			{"update_id":3,"edited_message":{"date":300,"chat":{"id":5,"type":"private","first_name":"Ann","last_name":"Lee","username":"ann"}}},
			{"update_id":4,"poll":{"id":"x"}}
		]}`)
	}
	w, chats := findChats(t, `{}`)
	if w.Code != http.StatusOK || len(chats) != 2 {
		t.Fatalf("status=%d chats=%+v", w.Code, chats)
	}
	if chats[0].ID != "5" || chats[0].Name != "Ann Lee" || chats[0].Username != "ann" || chats[0].LastSeen != 300 {
		t.Errorf("first chat = %+v", chats[0])
	}
	if chats[1].ID != "-1007" || chats[1].Name != "Garage" || chats[1].Type != "supergroup" {
		t.Errorf("second chat = %+v", chats[1])
	}
}

func TestFindChats_runningBotIsNotPolledOrStopped(t *testing.T) {
	f := newFindChatsFixture(t, savedTelegramToken)
	f.serviceAlive = true
	now := time.Now().Unix()
	recent := fmt.Sprintf(`[{"id":5,"type":"private","name":"Ann","username":"ann","last_seen":%d},
		{"id":6,"type":"private","name":"Old","last_seen":%d}]`, now, now-int64(2*telegramRecentChatsTTL/time.Second))
	if err := os.WriteFile(telegramRecentChatsPath, []byte(recent), 0o600); err != nil {
		t.Fatal(err)
	}
	w, chats := findChats(t, `{}`)
	if w.Code != http.StatusOK || len(chats) != 1 || chats[0].ID != "5" {
		t.Fatalf("status=%d chats=%+v", w.Code, chats)
	}
	if len(f.requests) != 0 {
		t.Errorf("a running bot must not be interrupted: %v", f.requests)
	}
	for _, action := range f.systemctl {
		if action != "is-active" {
			t.Errorf("service was changed with %q", action)
		}
	}
}

func TestFindChats_errorsAreClearAndNeverContainToken(t *testing.T) {
	cases := []struct {
		name, body, want string
		status, wantCode int
	}{
		{"conflict", `{"ok":false,"description":"Conflict: terminated by other getUpdates request"}`, "Another device", http.StatusConflict, http.StatusConflict},
		{"webhook", `{"ok":false,"description":"Conflict: can't use getUpdates method while webhook is active"}`, "webhook", http.StatusConflict, http.StatusConflict},
		{"bad token", `{"ok":false,"description":"Unauthorized"}`, "rejected this token", http.StatusUnauthorized, http.StatusBadRequest},
		{"rate limit", `{"ok":false,"description":"Too Many Requests"}`, "limiting", http.StatusTooManyRequests, http.StatusTooManyRequests},
		{"server error", `oops`, "returned 500", http.StatusInternalServerError, http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFindChatsFixture(t, savedTelegramToken)
			f.reply = func(w http.ResponseWriter) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}
			w, _ := findChats(t, `{}`)
			if w.Code != tc.wantCode || !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			if strings.Contains(w.Body.String(), savedTelegramToken) {
				t.Errorf("response contains the token: %s", w.Body)
			}
		})
	}
}

func TestFindChats_unreachableErrorHidesToken(t *testing.T) {
	newFindChatsFixture(t, savedTelegramToken)
	telegramAPIBase = "http://127.0.0.1:1"
	w, _ := findChats(t, `{}`)
	if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), savedTelegramToken) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
}

func TestFindChats_requiresValidToken(t *testing.T) {
	f := newFindChatsFixture(t, "")
	if w, _ := findChats(t, `{}`); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Paste your bot token") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if w, _ := findChats(t, `{"bot_token":"123/../evil"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if len(f.requests) != 0 {
		t.Errorf("invalid token reached Telegram: %v", f.requests)
	}
}
