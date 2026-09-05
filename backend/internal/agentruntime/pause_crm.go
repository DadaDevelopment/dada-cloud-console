package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// PauseCRM sets a configured status, with no operator or opportunity workflow.
type PauseCRM interface {
	SetPaused(context.Context, Conversation, string) error
}
type httpPauseCRM struct {
	url, token, status string
	client             *http.Client
}

func NewHTTPPauseCRM(endpoint, token, status string) PauseCRM {
	return &httpPauseCRM{endpoint, token, status, &http.Client{Timeout: 15 * time.Second}}
}
func (p *httpPauseCRM) SetPaused(ctx context.Context, conv Conversation, reason string) error {
	u, err := url.Parse(p.url)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || p.status == "" || p.token == "" {
		return fmt.Errorf("CRM pause integration is not configured")
	}
	body, _ := json.Marshal(map[string]any{"conversation_id": conv.ID, "agent_name": conv.AgentName, "channel": conv.Channel, "external_id": conv.ExternalID, "status": p.status, "reason": reason})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, p.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "agent-paused-"+conv.ID.String())
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("CRM pause request unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("CRM pause rejected: status %d", resp.StatusCode)
	}
	var result struct {
		Applied bool   `json:"applied"`
		Status  string `json:"status"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&result) != nil || !result.Applied || result.Status != p.status {
		return fmt.Errorf("CRM status not confirmed")
	}
	return nil
}
