package tggateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// TelegramUpdate is the fields this package cares about out of a Telegram
// Update object: a text message in a chat, plus the sender identity the
// agent needs to fill in CRM/logging tool calls (Username/FirstName are
// best-effort — Telegram lets either be empty). UserID is the sender's
// Telegram account id, distinct from the chat id in groups. HasLocation/
// Latitude/Longitude carry a native "Send location" share (Telegram's
// message.location field) — Text is empty on a pure location message.
//
// MessageID/SentAt/ReplyToMessageID/ThreadID (Agent Harness v2, Step 1) are
// Telegram's own message identity: MessageID becomes conversation_messages'
// channel_message_id, SentAt is message.date (when Telegram says the user
// actually sent it, distinct from when this poller observed it),
// ReplyToMessageID is message.reply_to_message.message_id (0 if not a
// reply), ThreadID is message.message_thread_id (0 outside a forum topic).
type TelegramUpdate struct {
	UpdateID         int64
	ChatID           int64
	UserID           int64
	Text             string
	Username         string
	FirstName        string
	HasLocation      bool
	Latitude         float64
	Longitude        float64
	MessageID        int64
	SentAt           time.Time
	ReplyToMessageID int64
	ThreadID         int64
}

// TelegramClient is the Bot API surface a poller needs. An interface so
// pollers and Manager.Bind can be tested against a fake HTTP server instead
// of api.telegram.org (design doc: "no real long-poll in CI"). GetMe
// validates a token and returns the bot's @username. GetUpdates long-polls
// starting at offset (the next unseen update id), passing timeoutSec through
// as Telegram's own long-poll timeout. SendMessage posts a plain-text reply
// into a chat. SendMessageWithLocationButton posts a reply with a native
// Telegram reply-keyboard button that shares the user's location in one tap
// (KeyboardButton.request_location) — Telegram delivers that share back as
// a message.location update, no text.
type TelegramClient interface {
	GetMe(ctx context.Context, token string) (username string, err error)
	GetUpdates(ctx context.Context, token string, offset int64, timeoutSec int) ([]TelegramUpdate, error)
	SendMessage(ctx context.Context, token string, chatID int64, text string) error
	SendMessageReply(ctx context.Context, token string, chatID int64, replyToMessageID int64, text string) error
	SendMessageWithLocationButton(ctx context.Context, token string, chatID int64, text string) error
	SendChatAction(ctx context.Context, token string, chatID int64, action string) error
}

// httpTelegramClient talks to the real Telegram Bot API (or a fake serving
// the same shape at a different baseURL, for tests).
type httpTelegramClient struct {
	baseURL string
	http    *http.Client
}

// telegramHTTPTimeout comfortably clears the longest GetUpdates long-poll
// (timeoutSec up to ~30s) plus network round-trip.
const telegramHTTPTimeout = 50 * time.Second

// NewTelegramClient builds a TelegramClient against baseURL (empty ->
// https://api.telegram.org).
func NewTelegramClient(baseURL string) TelegramClient {
	if baseURL == "" {
		baseURL = "https://api.telegram.org"
	}
	return &httpTelegramClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: telegramHTTPTimeout},
	}
}

type tgResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

func (c *httpTelegramClient) call(ctx context.Context, token, method string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = strings.NewReader(string(b))
	}
	url := fmt.Sprintf("%s/bot%s/%s", c.baseURL, token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	defer resp.Body.Close()

	var parsed tgResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("telegram %s: decode response: %w", method, err)
	}
	if !parsed.OK {
		return fmt.Errorf("telegram %s: %s", method, parsed.Description)
	}
	if out != nil {
		if err := json.Unmarshal(parsed.Result, out); err != nil {
			return fmt.Errorf("telegram %s: decode result: %w", method, err)
		}
	}
	return nil
}

func (c *httpTelegramClient) GetMe(ctx context.Context, token string) (string, error) {
	var out struct {
		Username string `json:"username"`
	}
	if err := c.call(ctx, token, "getMe", nil, &out); err != nil {
		return "", err
	}
	if out.Username == "" {
		return "", fmt.Errorf("telegram getMe: bot has no username")
	}
	return out.Username, nil
}

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		MessageID int64  `json:"message_id"`
		Date      int64  `json:"date"`
		Text      string `json:"text"`
		Location  *struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"location"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		From struct {
			ID        int64  `json:"id"`
			Username  string `json:"username"`
			FirstName string `json:"first_name"`
		} `json:"from"`
		ReplyToMessage *struct {
			MessageID int64 `json:"message_id"`
		} `json:"reply_to_message"`
		MessageThreadID int64 `json:"message_thread_id"`
	} `json:"message"`
}

func (c *httpTelegramClient) GetUpdates(ctx context.Context, token string, offset int64, timeoutSec int) ([]TelegramUpdate, error) {
	body := map[string]any{
		"offset":          offset,
		"timeout":         timeoutSec,
		"allowed_updates": []string{"message"},
	}
	var raw []tgUpdate
	if err := c.call(ctx, token, "getUpdates", body, &raw); err != nil {
		return nil, err
	}
	out := make([]TelegramUpdate, 0, len(raw))
	for _, u := range raw {
		if u.Message == nil {
			continue
		}
		hasLocation := u.Message.Location != nil
		if u.Message.Text == "" && !hasLocation {
			continue
		}
		upd := TelegramUpdate{
			UpdateID:  u.UpdateID,
			ChatID:    u.Message.Chat.ID,
			UserID:    u.Message.From.ID,
			Text:      u.Message.Text,
			Username:  u.Message.From.Username,
			FirstName: u.Message.From.FirstName,
			MessageID: u.Message.MessageID,
			ThreadID:  u.Message.MessageThreadID,
		}
		if u.Message.Date > 0 {
			upd.SentAt = time.Unix(u.Message.Date, 0).UTC()
		}
		if u.Message.ReplyToMessage != nil {
			upd.ReplyToMessageID = u.Message.ReplyToMessage.MessageID
		}
		if hasLocation {
			upd.HasLocation = true
			upd.Latitude = u.Message.Location.Latitude
			upd.Longitude = u.Message.Location.Longitude
		}
		out = append(out, upd)
	}
	return out, nil
}

func (c *httpTelegramClient) SendMessage(ctx context.Context, token string, chatID int64, text string) error {
	body := map[string]any{
		"chat_id": strconv.FormatInt(chatID, 10),
		"text":    text,
	}
	return c.call(ctx, token, "sendMessage", body, nil)
}

// SendMessageReply posts the text as a native Telegram reply to
// replyToMessageID (reply_parameters). allow_sending_without_reply lets a
// reply to a since-deleted message degrade to a plain message instead of
// failing the whole send -- the reply anchor is presentation, never worth
// losing the answer over.
func (c *httpTelegramClient) SendMessageReply(ctx context.Context, token string, chatID int64, replyToMessageID int64, text string) error {
	body := map[string]any{
		"chat_id": strconv.FormatInt(chatID, 10),
		"text":    text,
		"reply_parameters": map[string]any{
			"message_id":                   replyToMessageID,
			"allow_sending_without_reply": true,
		},
	}
	return c.call(ctx, token, "sendMessage", body, nil)
}

// locationRequestKeyboard is a Telegram ReplyKeyboardMarkup with one button
// whose request_location:true makes Telegram attach the user's device
// location and send it back as a message.location update when tapped — no
// permission prompt beyond Telegram's own OS-level location dialog, no text
// the user has to type. resize_keyboard shrinks it to fit the button instead
// of showing an oversized default keyboard.
func locationRequestKeyboard() map[string]any {
	return map[string]any{
		"keyboard": [][]map[string]any{
			{{"text": "📍 Отправить геолокацию", "request_location": true}},
		},
		"resize_keyboard":   true,
		"one_time_keyboard": false,
	}
}

func (c *httpTelegramClient) SendMessageWithLocationButton(ctx context.Context, token string, chatID int64, text string) error {
	body := map[string]any{
		"chat_id":      strconv.FormatInt(chatID, 10),
		"text":         text,
		"reply_markup": locationRequestKeyboard(),
	}
	return c.call(ctx, token, "sendMessage", body, nil)
}

func (c *httpTelegramClient) SendChatAction(ctx context.Context, token string, chatID int64, action string) error {
	body := map[string]any{
		"chat_id": strconv.FormatInt(chatID, 10),
		"action":  action,
	}
	return c.call(ctx, token, "sendChatAction", body, nil)
}
