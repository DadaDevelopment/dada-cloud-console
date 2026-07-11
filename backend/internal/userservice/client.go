package userservice

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

type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

type Client struct {
	baseURL string
	ts      TokenSource
	hc      *http.Client
}

func New(baseURL string, ts TokenSource) *Client {
	if baseURL == "" || ts == nil {
		return nil
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		ts:      ts,
		hc:      &http.Client{Timeout: 20 * time.Second},
	}
}

type ensureProjectBody struct {
	ProjectID        string `json:"project_id"`
	Slug             string `json:"slug"`
	DisplayName      string `json:"display_name"`
	OwnerPrincipalID string `json:"owner_principal_id,omitempty"`
}

func (c *Client) EnsureProjectGroups(ctx context.Context, orgID, projectID, slug, displayName, ownerSub string) error {
	if c == nil {
		return nil
	}
	if orgID == "" || projectID == "" {
		return fmt.Errorf("userservice: orgID and projectID are required")
	}
	tok, err := c.ts.Token(ctx)
	if err != nil {
		return fmt.Errorf("userservice: token: %w", err)
	}
	body, err := json.Marshal(ensureProjectBody{
		ProjectID:        projectID,
		Slug:             slug,
		DisplayName:      displayName,
		OwnerPrincipalID: ownerSub,
	})
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/orgs/%s/projects", c.baseURL, url.PathEscape(orgID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("userservice: ensure project groups: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("userservice: ensure project groups: status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}
