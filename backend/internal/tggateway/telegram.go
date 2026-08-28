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

// TelegramUpdate is the one field this package cares about out of a Telegram
// Update object: a text message in a chat.
type TelegramUpdate struct {
	UpdateID int64
	ChatID   int64
	Text     string
}

// TelegramClient is the Bot API surface a poller needs. An interface so
// pollers and Manager.Bind can be tested against a fake HTTP server instead
// of api.telegram.org (design doc: "no real long-poll in CI"). GetMe
// validates a token and returns the bot's @username. GetUpdates long-polls
// starting at offset (the next unseen update id), passing timeoutSec through
// as Telegram's own long-poll timeout. SendMessage posts a reply into a chat.
type TelegramClient interface {
	GetMe(ctx context.Context, token string) (username string, err error)
	GetUpdates(ctx context.Context, token string, offset int64, timeoutSec int) ([]TelegramUpdate, error)
	SendMessage(ctx context.Context, token string, chatID int64, text string) error
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
		Text string `json:"text"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
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
		if u.Message == nil || u.Message.Text == "" {
			continue
		}
		out = append(out, TelegramUpdate{UpdateID: u.UpdateID, ChatID: u.Message.Chat.ID, Text: u.Message.Text})
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

func (c *httpTelegramClient) SendChatAction(ctx context.Context, token string, chatID int64, action string) error {
	body := map[string]any{
		"chat_id": strconv.FormatInt(chatID, 10),
		"action":  action,
	}
	return c.call(ctx, token, "sendChatAction", body, nil)
}
