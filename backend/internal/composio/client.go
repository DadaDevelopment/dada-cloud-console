package composio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the Composio REST API this client speaks.
const DefaultBaseURL = "https://backend.composio.dev/api/v3.1"

// Client talks to Composio's tool-router API with the platform's own key.
//
// The key is a platform credential, never a tenant one: it authorizes every
// session of every user in the Composio project, which is exactly why the
// broker exists and why a session's MCP URL never reaches an agent manifest.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// NewClient returns a client for baseURL, or the public API when baseURL is empty.
func NewClient(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Session is the part of a created tool-router session the broker keeps.
type Session struct {
	ID     string
	MCPURL string
}

type sessionCreateResponse struct {
	SessionID string `json:"session_id"`
	MCP       struct {
		URL string `json:"url"`
	} `json:"mcp"`
}

// CreateSession opens a session for userID limited to toolkits.
//
// manage_connections is disabled: the connect flow belongs to the console, so
// an agent can never post a Connect Link of its own into a chat, and a toolkit
// the user has not authorized is simply absent rather than promptable.
func (c *Client) CreateSession(ctx context.Context, userID string, toolkits []string) (Session, error) {
	body := map[string]any{
		"user_id":            userID,
		"manage_connections": map[string]any{"enable": false},
	}
	if len(toolkits) > 0 {
		body["toolkits"] = map[string]any{"enable": toolkits}
	}
	var out sessionCreateResponse
	if err := c.do(ctx, http.MethodPost, "/tool_router/session", body, &out); err != nil {
		return Session{}, err
	}
	if out.SessionID == "" || out.MCP.URL == "" {
		return Session{}, fmt.Errorf("composio: session response carried no id or MCP url")
	}
	return Session{ID: out.SessionID, MCPURL: out.MCP.URL}, nil
}

// SetSessionToolkits rewrites a session's toolkit allowlist.
//
// Called when a user authorizes one more app: the session is reconfigured in
// place instead of a new one being minted, so the MCP URL an agent already holds
// keeps working and simply gains the new tools. The allowlist IS the
// authorization gate -- a toolkit absent here has no tools in the session, so a
// prompt cannot talk the agent into reaching an app the user never connected.
func (c *Client) SetSessionToolkits(ctx context.Context, sessionID string, toolkits []string) error {
	if toolkits == nil {
		toolkits = []string{}
	}
	body := map[string]any{"toolkits": map[string]any{"enable": toolkits}}
	path := "/tool_router/session/" + url.PathEscape(sessionID)
	return c.do(ctx, http.MethodPatch, path, body, nil)
}

// ToolkitConnection is one toolkit's connection state inside a session.
type ToolkitConnection struct {
	Slug               string `json:"slug"`
	Name               string `json:"name"`
	Active             bool   `json:"active"`
	Status             string `json:"status,omitempty"`
	ConnectedAccountID string `json:"connected_account_id,omitempty"`
	LogoURL            string `json:"logo_url,omitempty"`
}

type toolkitsResponse struct {
	Items []struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
		Meta struct {
			Logo string `json:"logo"`
		} `json:"meta"`
		ConnectedAccount *struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"connected_account"`
	} `json:"items"`
}

// SessionToolkits reports, per toolkit of the session, whether this user has a
// live connected account.
//
// A connected account exists from the moment a Connect Link is minted, so
// presence proves nothing: only status ACTIVE means the tools actually run. An
// INITIALIZING row is a link the user opened and never finished, and reporting
// it as connected would offer an agent tools that answer 401.
func (c *Client) SessionToolkits(ctx context.Context, sessionID string) ([]ToolkitConnection, error) {
	var out toolkitsResponse
	path := "/tool_router/session/" + url.PathEscape(sessionID) + "/toolkits"
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	conns := make([]ToolkitConnection, 0, len(out.Items))
	for _, item := range out.Items {
		conn := ToolkitConnection{Slug: item.Slug, Name: item.Name, LogoURL: item.Meta.Logo}
		if item.ConnectedAccount != nil {
			conn.ConnectedAccountID = item.ConnectedAccount.ID
			conn.Status = item.ConnectedAccount.Status
			conn.Active = strings.EqualFold(item.ConnectedAccount.Status, "ACTIVE")
		}
		conns = append(conns, conn)
	}
	return conns, nil
}

// ConnectLink is a hosted authorization page for one toolkit and one user.
type ConnectLink struct {
	Token              string `json:"link_token"`
	RedirectURL        string `json:"redirect_url"`
	ConnectedAccountID string `json:"connected_account_id"`
}

// Authorize asks Composio for a Connect Link for toolkit in this session.
//
// callbackURL is where the user lands afterwards; Composio appends status and
// connected_account_id to it, which is how the console learns the outcome
// without polling a connection it cannot see.
func (c *Client) Authorize(ctx context.Context, sessionID, toolkit, callbackURL string) (ConnectLink, error) {
	body := map[string]any{"toolkit": toolkit}
	if callbackURL != "" {
		body["callback_url"] = callbackURL
	}
	var out ConnectLink
	path := "/tool_router/session/" + url.PathEscape(sessionID) + "/link"
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return ConnectLink{}, err
	}
	if out.RedirectURL == "" {
		return ConnectLink{}, fmt.Errorf("composio: authorize %s returned no redirect url", toolkit)
	}
	return out, nil
}

// ConnectedAccount is one authorized account of one user.
type ConnectedAccount struct {
	ID      string `json:"id"`
	Toolkit string `json:"toolkit"`
	Status  string `json:"status"`
	UserID  string `json:"user_id"`
}

type connectedAccountsResponse struct {
	Items []struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		UserID  string `json:"user_id"`
		Toolkit struct {
			Slug string `json:"slug"`
		} `json:"toolkit"`
	} `json:"items"`
}

// ConnectedAccounts lists every account of one user, across sessions.
//
// This is the authoritative source for "is this toolkit usable": a session's
// toolkit view is scoped to that session's filter, while a user's accounts
// outlive every session that created them.
func (c *Client) ConnectedAccounts(ctx context.Context, userID string) ([]ConnectedAccount, error) {
	var out connectedAccountsResponse
	path := "/connected_accounts?user_ids=" + url.QueryEscape(userID)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	accounts := make([]ConnectedAccount, 0, len(out.Items))
	for _, item := range out.Items {
		accounts = append(accounts, ConnectedAccount{
			ID:      item.ID,
			Toolkit: item.Toolkit.Slug,
			Status:  item.Status,
			UserID:  item.UserID,
		})
	}
	return accounts, nil
}

// Toolkit is one entry of the Composio catalog as the console offers it.
type Toolkit struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	LogoURL string `json:"logo_url,omitempty"`
	NoAuth  bool   `json:"no_auth"`
}

type catalogResponse struct {
	Items []struct {
		Slug   string `json:"slug"`
		Name   string `json:"name"`
		NoAuth bool   `json:"no_auth"`
		Meta   struct {
			Logo string `json:"logo"`
		} `json:"meta"`
	} `json:"items"`
}

// Catalog returns catalog entries keyed by slug, filtered to slugs when given,
// so the console can show a real name and logo instead of a slug.
func (c *Client) Catalog(ctx context.Context, slugs []string) (map[string]Toolkit, error) {
	var out catalogResponse
	if err := c.do(ctx, http.MethodGet, "/toolkits?limit=500", nil, &out); err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, s := range slugs {
		want[s] = true
	}
	found := map[string]Toolkit{}
	for _, item := range out.Items {
		if len(want) > 0 && !want[item.Slug] {
			continue
		}
		found[item.Slug] = Toolkit{Slug: item.Slug, Name: item.Name, LogoURL: item.Meta.Logo, NoAuth: item.NoAuth}
	}
	return found, nil
}

// APIError is a non-2xx answer from Composio, kept whole so a failure names the
// upstream reason instead of a bare status.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("composio: status %d: %s", e.Status, e.Body)
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("composio %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(raw))}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("composio %s %s: decode: %w", method, path, err)
	}
	return nil
}
