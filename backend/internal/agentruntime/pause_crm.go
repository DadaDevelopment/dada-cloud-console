package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// ErrCRMPauseRejected marks an answer that no retry can change: the CRM
// endpoint refused the request itself (422 for an external id it cannot
// map, 404 for a person it never linked). The reconciler stores it as the
// terminal "rejected" status instead of retrying it every 30 seconds for good,
// as it did for a conversation with external id "-900679279482B" from
// 2026-09-13 to 2026-09-24.
var ErrCRMPauseRejected = errors.New("CRM pause rejected permanently")

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
	if permanentPauseRejection(resp.StatusCode) {
		return fmt.Errorf("%w: status %d", ErrCRMPauseRejected, resp.StatusCode)
	}
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

// permanentPauseRejection reports a 4xx the same request will get again.
// 408 and 429 are about timing, and 401/403 about this runtime's token, which
// a redeploy fixes; those stay retryable so no pause is dropped over them.
func permanentPauseRejection(code int) bool {
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestTimeout, http.StatusTooManyRequests:
		return false
	}
	return code >= 400 && code < 500
}
