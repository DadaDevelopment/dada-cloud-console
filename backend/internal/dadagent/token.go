// Package dadagent is the cloud-side client for the DadaAgent: a Keycloak
// client-credentials token source plus an intent submit/execute/file client.
package dadagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TokenSource fetches and caches a Keycloak client-credentials access token.
type TokenSource struct {
	tokenURL string
	clientID string
	secret   string
	hc       *http.Client

	mu  sync.Mutex
	tok string
	exp time.Time
}

// NewTokenSource builds a token source for the given Keycloak token endpoint.
func NewTokenSource(tokenURL, clientID, secret string) *TokenSource {
	return &TokenSource{
		tokenURL: tokenURL, clientID: clientID, secret: secret,
		hc: &http.Client{Timeout: 15 * time.Second},
	}
}

// Token returns a cached token, refreshing when within 30s of expiry.
func (ts *TokenSource) Token(ctx context.Context) (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.tok != "" && time.Now().Before(ts.exp.Add(-30*time.Second)) {
		return ts.tok, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {ts.clientID},
		"client_secret": {ts.secret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := ts.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("keycloak token: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("keycloak token: status %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	ts.tok = out.AccessToken
	ts.exp = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	return ts.tok, nil
}
