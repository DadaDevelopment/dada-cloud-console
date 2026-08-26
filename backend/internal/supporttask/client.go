// Package supporttask talks to AgentSyncHub's cloud-only support intake, the
// single write path that turns a customer's feedback ticket into a kanban
// card. The wire shape is deliberately closed: support_task_id, title,
// report, requester, project_key, app_name only. No prompt, no skill choice,
// no callback URL travel from here -- the untrusted ticket text only ever
// becomes report, never an instruction the agent side would execute.
package supporttask

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Request is the closed, bounded intake payload. Field limits mirror the
// AgentSyncHub-side Pydantic model (SupportIntakeIn) so a caller cannot rely
// on the network to catch what the schema already rejects.
type Request struct {
	SupportTaskID string `json:"support_task_id"`
	Title         string `json:"title"`
	Report        string `json:"report"`
	Requester     string `json:"requester,omitempty"`
	ProjectKey    string `json:"project_key,omitempty"`
	AppName       string `json:"app_name,omitempty"`
}

// Result is the trimmed-down response: the kanban card id and whether this
// call created it (false means an earlier retry already landed the card).
type Result struct {
	ID      string
	Created bool
}

// TokenSource mints the bearer this client sends; supplied by the caller so
// this package carries no Keycloak client of its own (reuse dadagent's).
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// Client posts one ticket at a time to a fixed, configured intake route.
type Client struct {
	baseURL string
	ts      TokenSource
	hc      *http.Client
}

// New builds a support-intake client. Returns nil when unconfigured, exactly
// like dadagent.New, so callers can no-op a disabled integration instead of
// nil-checking a zero-value client at every call site.
func New(baseURL string, ts TokenSource) *Client {
	if baseURL == "" || ts == nil {
		return nil
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		ts:      ts,
		// Short, bounded: this call is on the SubmitFeedback request path and
		// must not hold an HTTP handler open on a stalled kanban backend.
		hc: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return fmt.Errorf("support intake: redirects are not followed")
			},
		},
	}
}

// Intake files one ticket. Idempotent by SupportTaskID: a retried call with
// the same id returns the same Result.ID with Created=false.
func (c *Client) Intake(ctx context.Context, in Request) (Result, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/support/intake", bytes.NewReader(b))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	tok, err := c.ts.Token(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("support intake: token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := c.hc.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("support intake: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return Result{}, fmt.Errorf("support intake: status %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		ID      string `json:"id"`
		Created bool   `json:"created"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Result{}, fmt.Errorf("support intake: decode: %w", err)
	}
	return Result{ID: out.ID, Created: out.Created}, nil
}
