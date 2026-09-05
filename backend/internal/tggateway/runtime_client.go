package tggateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type RuntimeClient interface {
	ProcessMessage(ctx context.Context, req RuntimeMessageRequest) (RuntimeMessageResponse, error)
}

// RuntimeMessageRequest is the payload tg-gateway posts to agent-runtime.
// ChannelMessageID/ThreadID/SourceSentAt/ReplyToChannelMessageID (Agent
// Harness v2, Step 1) carry Telegram's own message identity through to the
// canonical Message row; all four are optional passthroughs of
// TelegramUpdate's corresponding fields.
type RuntimeMessageRequest struct {
	AgentName               string                  `json:"agent_name"`
	Channel                 string                  `json:"channel"`
	ExternalID              string                  `json:"external_id"`
	Actor                   RuntimeActor            `json:"actor"`
	Content                 string                  `json:"content,omitempty"`
	ChannelMessageID        string                  `json:"channel_message_id,omitempty"`
	ThreadID                string                  `json:"thread_id,omitempty"`
	SourceSentAt            *time.Time              `json:"source_sent_at,omitempty"`
	ReplyToChannelMessageID string                  `json:"reply_to_channel_message_id,omitempty"`
	Messages                []RuntimeInboundMessage `json:"messages,omitempty"`
}

// RuntimeInboundMessage is one message of a debounced batch (Agent Harness
// v2, Step 2): the gateway aggregates rapid-fire messages into one request,
// but each keeps its own channel identity so the runtime persists every one
// as a separate Message row. Links (Step 5) carries the message's URL
// entities with their resolved titles -- extraction/enrichment is the
// gateway's deterministic job, the runtime just persists and renders them.
type RuntimeInboundMessage struct {
	Content                 string            `json:"content"`
	ChannelMessageID        string            `json:"channel_message_id,omitempty"`
	ThreadID                string            `json:"thread_id,omitempty"`
	SourceSentAt            *time.Time        `json:"source_sent_at,omitempty"`
	ReplyToChannelMessageID string            `json:"reply_to_channel_message_id,omitempty"`
	Links                   []RuntimeLinkMeta `json:"links,omitempty"`
}

// RuntimeLinkMeta is one URL found in a message plus its best-effort page
// title (empty when the fetch failed or the site was slow -- the URL alone
// still flows).
type RuntimeLinkMeta struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

type RuntimeActor struct {
	ExternalID string         `json:"external_id"`
	Username   string         `json:"username"`
	Metadata   map[string]any `json:"metadata"`
}

type RuntimeMessageResponse struct {
	Text                    string `json:"text"`
	ReplyToChannelMessageID string `json:"reply_to_channel_message_id,omitempty"`
	Suppressed              bool   `json:"suppressed,omitempty"`
}

type httpRuntimeClient struct {
	http    *http.Client
	baseURL string
	token   string
}

func NewRuntimeClient(baseURL string) RuntimeClient {
	return NewAuthenticatedRuntimeClient(baseURL, "")
}

func NewAuthenticatedRuntimeClient(baseURL, token string) RuntimeClient {
	if baseURL == "" {
		baseURL = "http://agent-runtime.dada-cloud.svc.cluster.local:8083"
	}
	return &httpRuntimeClient{
		http:    &http.Client{Timeout: 120 * time.Second},
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
	}
}

// NewRuntimeClientFromConfig keeps runtime routing opt-in, but fails startup
// for incomplete configuration rather than silently bypassing lifecycle state.
func NewRuntimeClientFromConfig(baseURL, token string) (RuntimeClient, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, nil
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("AGENT_RUNTIME_TOKEN is required when AGENT_RUNTIME_URL is set")
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("AGENT_RUNTIME_URL must be an HTTP(S) base URL without credentials, query or fragment")
	}
	return NewAuthenticatedRuntimeClient(baseURL, token), nil
}

func (c *httpRuntimeClient) ProcessMessage(ctx context.Context, req RuntimeMessageRequest) (RuntimeMessageResponse, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return RuntimeMessageResponse{}, err
	}

	url := c.baseURL + "/message"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return RuntimeMessageResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return RuntimeMessageResponse{}, fmt.Errorf("runtime request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp map[string]string
		json.NewDecoder(resp.Body).Decode(&errResp)
		return RuntimeMessageResponse{}, fmt.Errorf("runtime status %d: %s", resp.StatusCode, errResp["error"])
	}

	var result RuntimeMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return RuntimeMessageResponse{}, fmt.Errorf("decode runtime response: %w", err)
	}

	return result, nil
}

type noopRuntimeClient struct{}

func NewNoopRuntimeClient() RuntimeClient {
	return &noopRuntimeClient{}
}

func (c *noopRuntimeClient) ProcessMessage(ctx context.Context, req RuntimeMessageRequest) (RuntimeMessageResponse, error) {
	return RuntimeMessageResponse{}, fmt.Errorf("runtime client disabled")
}

// ParseRuntimeAgents requires an explicit rollout scope when runtime is enabled.
func ParseRuntimeAgents(value string, enabled bool) ([]string, error) {
	var names []string
	for _, name := range strings.Split(value, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	if enabled && len(names) == 0 {
		return nil, fmt.Errorf("AGENT_RUNTIME_AGENTS must name the bindings routed through runtime")
	}
	return names, nil
}
