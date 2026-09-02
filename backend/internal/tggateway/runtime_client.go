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

type RuntimeMessageRequest struct {
	AgentName  string       `json:"agent_name"`
	Channel    string       `json:"channel"`
	ExternalID string       `json:"external_id"`
	Actor      RuntimeActor `json:"actor"`
	Content    string       `json:"content"`
}

type RuntimeActor struct {
	ExternalID string         `json:"external_id"`
	Username   string         `json:"username"`
	Metadata   map[string]any `json:"metadata"`
}

type RuntimeMessageResponse struct {
	Text string `json:"text"`
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
