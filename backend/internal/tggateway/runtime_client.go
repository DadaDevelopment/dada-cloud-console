package tggateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
// as a separate Message row.
type RuntimeInboundMessage struct {
	Content                 string     `json:"content"`
	ChannelMessageID        string     `json:"channel_message_id,omitempty"`
	ThreadID                string     `json:"thread_id,omitempty"`
	SourceSentAt            *time.Time `json:"source_sent_at,omitempty"`
	ReplyToChannelMessageID string     `json:"reply_to_channel_message_id,omitempty"`
}

type RuntimeActor struct {
	ExternalID string         `json:"external_id"`
	Username   string         `json:"username"`
	Metadata   map[string]any `json:"metadata"`
}

type RuntimeMessageResponse struct {
	Text                    string `json:"text"`
	ReplyToChannelMessageID string `json:"reply_to_channel_message_id,omitempty"`
}

type httpRuntimeClient struct {
	http    *http.Client
	baseURL string
}

func NewRuntimeClient(baseURL string) RuntimeClient {
	if baseURL == "" {
		baseURL = "http://agent-runtime.dada-cloud.svc.cluster.local:8083"
	}
	return &httpRuntimeClient{
		http:    &http.Client{Timeout: 120 * time.Second},
		baseURL: baseURL,
	}
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

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return RuntimeMessageResponse{}, fmt.Errorf("runtime request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
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
