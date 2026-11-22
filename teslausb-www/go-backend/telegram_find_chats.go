package main

// telegram_find_chats.go — list chats that recently messaged the user's bot,
// so Settings can fill in the Chat ID.
//
// POST /api/config/telegram/find-chats   {"bot_token": "<optional unsaved token>"}
//
// The result is only a suggestion: nothing is saved here, because the chat ID
// is the bot's allowlist and anyone who finds a bot can message it.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	telegramAPIBase         = "https://api.telegram.org"
	telegramRecentChatsPath = "/run/teslausb/telegram_recent_chats.json"
	telegramFindChatsMu     sync.Mutex
)

const (
	telegramMaxFoundChats  = 10
	telegramRecentChatsTTL = time.Hour
)

// telegramFoundChat is one chat the user can choose from.
type telegramFoundChat struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Username string `json:"username,omitempty"`
	LastSeen int64  `json:"last_seen"`
}

type telegramAPIChat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type telegramAPIMessage struct {
	Date int64           `json:"date"`
	Chat telegramAPIChat `json:"chat"`
}

type telegramAPIUpdate struct {
	Message           *telegramAPIMessage `json:"message"`
	EditedMessage     *telegramAPIMessage `json:"edited_message"`
	ChannelPost       *telegramAPIMessage `json:"channel_post"`
	EditedChannelPost *telegramAPIMessage `json:"edited_channel_post"`
	CallbackQuery     *struct {
		Message *telegramAPIMessage `json:"message"`
	} `json:"callback_query"`
	MyChatMember *telegramAPIMessage `json:"my_chat_member"`
}

func (u telegramAPIUpdate) message() *telegramAPIMessage {
	for _, m := range []*telegramAPIMessage{u.Message, u.EditedMessage, u.ChannelPost, u.EditedChannelPost, u.MyChatMember} {
		if m != nil {
			return m
		}
	}
	if u.CallbackQuery != nil {
		return u.CallbackQuery.Message
	}
	return nil
}

func telegramChatName(c telegramAPIChat) string {
	if c.Title != "" {
		return c.Title
	}
	return strings.TrimSpace(c.FirstName + " " + c.LastName)
}

// newestTelegramChats keeps the latest sighting of each chat, newest first.
func newestTelegramChats(chats []telegramFoundChat) []telegramFoundChat {
	latest := make(map[string]telegramFoundChat, len(chats))
	for _, c := range chats {
		if existing, ok := latest[c.ID]; !ok || c.LastSeen >= existing.LastSeen {
			latest[c.ID] = c
		}
	}
	result := make([]telegramFoundChat, 0, len(latest))
	for _, c := range latest {
		result = append(result, c)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].LastSeen != result[j].LastSeen {
			return result[i].LastSeen > result[j].LastSeen
		}
		return result[i].ID < result[j].ID
	})
	if len(result) > telegramMaxFoundChats {
		result = result[:telegramMaxFoundChats]
	}
	return result
}

type telegramLookupError struct {
	status  int
	message string
}

func (e *telegramLookupError) Error() string { return e.message }

// fetchTelegramChats reads the bot's pending updates. It sends no offset, which
// is what would acknowledge them, and no allowed_updates, which Telegram would
// remember for the bot, so the bot can still read every message afterwards.
func fetchTelegramChats(token string) ([]telegramFoundChat, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(telegramAPIBase + "/bot" + token + "/getUpdates?limit=100&timeout=0")
	if err != nil {
		return nil, &telegramLookupError{http.StatusBadGateway, "Telegram API unreachable: " + requestErrorReason(err)}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, &telegramLookupError{http.StatusBadGateway, "could not read Telegram's reply"}
	}

	var reply struct {
		OK          bool                `json:"ok"`
		Description string              `json:"description"`
		Result      []telegramAPIUpdate `json:"result"`
	}
	decodeErr := json.Unmarshal(body, &reply)

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusNotFound:
		return nil, &telegramLookupError{http.StatusBadRequest, "Telegram rejected this token. Check it and try again."}
	case resp.StatusCode == http.StatusConflict:
		if strings.Contains(strings.ToLower(reply.Description), "webhook") {
			return nil, &telegramLookupError{http.StatusConflict, "This bot uses a webhook, so its messages can't be read here. Enter the chat ID manually."}
		}
		return nil, &telegramLookupError{http.StatusConflict, "Another device is reading this bot's messages. Stop it and try again, or enter the chat ID manually."}
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, &telegramLookupError{http.StatusTooManyRequests, "Telegram is limiting requests. Wait a minute and try again."}
	case resp.StatusCode != http.StatusOK || decodeErr != nil || !reply.OK:
		return nil, &telegramLookupError{http.StatusBadGateway, fmt.Sprintf("Telegram API returned %d", resp.StatusCode)}
	}

	chats := make([]telegramFoundChat, 0, len(reply.Result))
	for _, update := range reply.Result {
		m := update.message()
		if m == nil || m.Chat.ID == 0 {
			continue
		}
		chats = append(chats, telegramFoundChat{
			ID:       strconv.FormatInt(m.Chat.ID, 10),
			Type:     m.Chat.Type,
			Name:     telegramChatName(m.Chat),
			Username: m.Chat.Username,
			LastSeen: m.Date,
		})
	}
	return newestTelegramChats(chats), nil
}

// readRunningBotChats returns the chats the running bot saw recently. While it
// runs, it holds Telegram's only update stream and acknowledges each message,
// so polling here would interrupt it and find nothing.
func readRunningBotChats(now time.Time) ([]telegramFoundChat, error) {
	data, err := os.ReadFile(telegramRecentChatsPath)
	if errors.Is(err, os.ErrNotExist) {
		return []telegramFoundChat{}, nil
	}
	if err != nil {
		return nil, err
	}
	var stored []struct {
		ID       int64  `json:"id"`
		Type     string `json:"type"`
		Name     string `json:"name"`
		Username string `json:"username"`
		LastSeen int64  `json:"last_seen"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return []telegramFoundChat{}, nil
	}
	cutoff := now.Add(-telegramRecentChatsTTL).Unix()
	chats := make([]telegramFoundChat, 0, len(stored))
	for _, c := range stored {
		if c.ID == 0 || c.LastSeen < cutoff {
			continue
		}
		chats = append(chats, telegramFoundChat{
			ID:       strconv.FormatInt(c.ID, 10),
			Type:     c.Type,
			Name:     c.Name,
			Username: c.Username,
			LastSeen: c.LastSeen,
		})
	}
	return newestTelegramChats(chats), nil
}

// HandleTelegramFindChats — POST /api/config/telegram/find-chats
func HandleTelegramFindChats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if !telegramFindChatsMu.TryLock() {
		writeJSONError(w, http.StatusConflict, "already looking for chats")
		return
	}
	defer telegramFindChatsMu.Unlock()

	var req struct {
		BotToken string `json:"bot_token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	savedToken, _, _, err := readTelegramEnv()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not read the saved token")
		return
	}
	token := strings.TrimSpace(req.BotToken)
	if token == "" {
		token = savedToken
	}
	if token == "" {
		writeJSONError(w, http.StatusBadRequest, "Paste your bot token first.")
		return
	}
	if !telegramTokenRe.MatchString(token) {
		writeJSONError(w, http.StatusBadRequest, "token must look like 123456789:AA… as given by BotFather, without a \"bot\" prefix")
		return
	}

	var chats []telegramFoundChat
	if token == savedToken && systemctlIs("is-active", telegramServiceName) {
		chats, err = readRunningBotChats(time.Now())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "could not read the bot's recent chats")
			return
		}
	} else {
		chats, err = fetchTelegramChats(token)
		if err != nil {
			var lookupErr *telegramLookupError
			if errors.As(err, &lookupErr) {
				writeJSONError(w, lookupErr.status, lookupErr.message)
			} else {
				writeJSONError(w, http.StatusBadGateway, "could not reach Telegram")
			}
			return
		}
	}
	_ = json.NewEncoder(w).Encode(map[string][]telegramFoundChat{"chats": chats})
}
