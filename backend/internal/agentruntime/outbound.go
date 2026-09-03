package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// httpChannelOutbound delivers proactive replies to tg-gateway's internal
// POST /outbound endpoint (ClusterIP-only, no auth -- same posture as the
// runtime's own API).
type httpChannelOutbound struct {
	baseURL string
	http    *http.Client
}

func NewHTTPChannelOutbound(baseURL string) ChannelOutbound {
	return &httpChannelOutbound{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *httpChannelOutbound) SendOutbound(ctx context.Context, agentName, chatExternalID, text, replyToChannelMessageID string) error {
	payload, err := json.Marshal(map[string]string{
		"agent_name":                    agentName,
		"chat_id":                       chatExternalID,
		"text":                          text,
		"reply_to_channel_message_id":   replyToChannelMessageID,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/outbound", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("outbound request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("outbound status %d", resp.StatusCode)
	}
	return nil
}
