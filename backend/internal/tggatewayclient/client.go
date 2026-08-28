// Package tggatewayclient is a thin proxy client for tg-gateway's internal
// HTTP API (backend/internal/tggateway/server.go): POST/DELETE/GET
// /bindings. tg-gateway is cluster-internal and ClusterIP-only, the same
// posture kagent.Reader has toward the kagent runtime -- the console backend
// never touches Telegram or tg_bindings directly, it proxies through here.
// New returns nil when unconfigured so callers can treat Telegram binding as
// disabled (503), mirroring internal/buildagent.
package tggatewayclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrInvalidToken is returned by Bind when tg-gateway's getMe validation
// rejected the token (tg-gateway answers 400).
var ErrInvalidToken = errors.New("tggatewayclient: invalid bot token")

// ErrNotFound is returned by Get when the agent has no binding (tg-gateway
// answers 404).
var ErrNotFound = errors.New("tggatewayclient: no binding for that agent")

// Client is a proxy client for tg-gateway's internal API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New creates a tg-gateway proxy client. Returns nil if unconfigured so
// callers can treat Telegram binding as disabled.
func New(baseURL string) *Client {
	if baseURL == "" {
		return nil
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

// Binding is the shape tg-gateway's bind/get responses share.
type Binding struct {
	Bound       bool   `json:"bound"`
	BotUsername string `json:"bot_username"`
}

// Bind proxies POST /bindings.
func (c *Client) Bind(ctx context.Context, agentName, projectID, botToken string) (Binding, error) {
	body, err := json.Marshal(map[string]string{
		"agent_name": agentName,
		"project_id": projectID,
		"bot_token":  botToken,
	})
	if err != nil {
		return Binding{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bindings", strings.NewReader(string(body)))
	if err != nil {
		return Binding{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Binding{}, fmt.Errorf("tg-gateway unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadRequest {
		return Binding{}, ErrInvalidToken
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return Binding{}, fmt.Errorf("tg-gateway bind: status %d: %s", resp.StatusCode, string(b))
	}
	var out Binding
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Binding{}, err
	}
	out.Bound = true
	return out, nil
}

// Unbind proxies DELETE /bindings/{agentName}.
func (c *Client) Unbind(ctx context.Context, agentName string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/bindings/"+agentName, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("tg-gateway unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("tg-gateway unbind: status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// Get proxies GET /bindings/{agentName}. Returns ErrNotFound when unbound.
func (c *Client) Get(ctx context.Context, agentName string) (Binding, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/bindings/"+agentName, nil)
	if err != nil {
		return Binding{}, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Binding{}, fmt.Errorf("tg-gateway unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return Binding{Bound: false}, ErrNotFound
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return Binding{}, fmt.Errorf("tg-gateway get: status %d: %s", resp.StatusCode, string(b))
	}
	var out Binding
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Binding{}, err
	}
	return out, nil
}
