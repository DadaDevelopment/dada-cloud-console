package yookassa

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// defaultOAuthBaseURL is the production YooKassa Partners OAuth root.
const defaultOAuthBaseURL = "https://yookassa.ru/oauth/v2"

// OAuthClient talks to the YooKassa Partners API OAuth flow: authorize URL,
// code exchange, merchant identity lookup, and per-token webhook management.
// It is a separate client from Client (which signs requests with a fixed
// shop id + secret key) because every OAuth-connected merchant carries its
// own bearer token. OAuthBaseURL and APIBaseURL are exported so tests can
// point both at httptest.Server instances independently.
type OAuthClient struct {
	OAuthBaseURL string
	APIBaseURL   string
	HTTPClient   *http.Client
}

// NewOAuthClient builds an OAuthClient against the production YooKassa hosts.
func NewOAuthClient() *OAuthClient {
	return &OAuthClient{
		OAuthBaseURL: defaultOAuthBaseURL,
		APIBaseURL:   defaultBaseURL,
		HTTPClient:   http.DefaultClient,
	}
}

func (c *OAuthClient) oauthBaseURL() string {
	if c.OAuthBaseURL != "" {
		return c.OAuthBaseURL
	}
	return defaultOAuthBaseURL
}

func (c *OAuthClient) apiBaseURL() string {
	if c.APIBaseURL != "" {
		return c.APIBaseURL
	}
	return defaultBaseURL
}

func (c *OAuthClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// AuthorizeURL builds the YooKassa Partners authorize URL for clientID/state,
// against the OAuthClient's (possibly injected, for tests) OAuthBaseURL. No
// redirect_uri is sent -- the callback URL is bound at partner-app
// registration; no scope is sent -- rights are chosen at registration.
func (c *OAuthClient) AuthorizeURL(clientID, state string) string {
	return fmt.Sprintf("%s/authorize?response_type=code&client_id=%s&state=%s",
		c.oauthBaseURL(), url.QueryEscape(clientID), url.QueryEscape(state))
}

// tokenResponse is the body of POST /oauth/v2/token. YooKassa returns no
// refresh_token -- expiry means re-authorize, there is no refresh path.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

// ExchangeCode exchanges an authorization code for a bearer access token.
// Authenticated with HTTP Basic (clientID:clientSecret), body form-encoded
// grant_type=authorization_code&code=<code>.
func (c *OAuthClient) ExchangeCode(ctx context.Context, clientID, clientSecret, code string) (accessToken string, expiresIn int64, err error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.oauthBaseURL()+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("yookassa oauth: build token request: %w", err)
	}
	req.SetBasicAuth(clientID, clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("yookassa oauth: token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("yookassa oauth: read token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, &Error{StatusCode: resp.StatusCode, Description: string(body)}
	}

	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", 0, fmt.Errorf("yookassa oauth: decode token response: %w", err)
	}
	if tok.AccessToken == "" {
		return "", 0, fmt.Errorf("yookassa oauth: token response missing access_token")
	}
	return tok.AccessToken, tok.ExpiresIn, nil
}

// meFields is the tolerant shape of GET /v3/me. The real schema is
// unconfirmed, so every known identity field is tried in order and the raw
// body is always kept alongside for later inspection.
type meFields struct {
	AccountID string `json:"account_id"`
	ID        string `json:"id"`
	ShopID    string `json:"shop_id"`
}

// Me fetches the merchant identity for accessToken. accountID is the first
// non-empty of account_id, id, shop_id (in that order); raw is the full
// response body, stored verbatim so a schema this client does not yet parse
// is never lost.
func (c *OAuthClient) Me(ctx context.Context, accessToken string) (accountID string, raw json.RawMessage, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBaseURL()+"/me", nil)
	if err != nil {
		return "", nil, fmt.Errorf("yookassa oauth: build me request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("yookassa oauth: me request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("yookassa oauth: read me response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, &Error{StatusCode: resp.StatusCode, Description: string(body)}
	}

	var fields meFields
	if jsonErr := json.Unmarshal(body, &fields); jsonErr == nil {
		switch {
		case fields.AccountID != "":
			accountID = fields.AccountID
		case fields.ID != "":
			accountID = fields.ID
		case fields.ShopID != "":
			accountID = fields.ShopID
		}
	}
	return accountID, json.RawMessage(body), nil
}

// webhookRequest is the body of POST /v3/webhooks.
type webhookRequest struct {
	Event string `json:"event"`
	URL   string `json:"url"`
}

// webhookResponse is the subset of the created webhook object read back.
type webhookResponse struct {
	ID string `json:"id"`
}

// RegisterWebhook registers a webhook subscription for event on url, scoped
// to accessToken's merchant (events created by other tokens never fire it).
// A fresh Idempotence-Key is generated per call.
func (c *OAuthClient) RegisterWebhook(ctx context.Context, accessToken, event, webhookURL string) (id string, err error) {
	body, err := json.Marshal(webhookRequest{Event: event, URL: webhookURL})
	if err != nil {
		return "", fmt.Errorf("yookassa oauth: marshal webhook request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBaseURL()+"/webhooks", strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("yookassa oauth: build webhook request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotence-Key", uuid.New().String())

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("yookassa oauth: webhook request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("yookassa oauth: read webhook response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &Error{StatusCode: resp.StatusCode, Description: string(respBody)}
	}

	var wh webhookResponse
	if err := json.Unmarshal(respBody, &wh); err != nil {
		return "", fmt.Errorf("yookassa oauth: decode webhook response: %w", err)
	}
	return wh.ID, nil
}

// DeleteWebhook removes a webhook subscription previously created by
// RegisterWebhook, scoped to accessToken's merchant. A fresh Idempotence-Key
// is generated per call.
func (c *OAuthClient) DeleteWebhook(ctx context.Context, accessToken, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.apiBaseURL()+"/webhooks/"+id, nil)
	if err != nil {
		return fmt.Errorf("yookassa oauth: build delete-webhook request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Idempotence-Key", uuid.New().String())

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("yookassa oauth: delete-webhook request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &Error{StatusCode: resp.StatusCode, Description: string(body)}
	}
	return nil
}

// EncodeAccessToken base64-encodes an access token for storage in the TEXT
// access_token_enc column after AES-GCM encryption (crypto.EncryptToken
// returns raw bytes; the column is TEXT, not BYTEA).
func EncodeAccessToken(enc []byte) string {
	return base64.StdEncoding.EncodeToString(enc)
}

// DecodeAccessToken reverses EncodeAccessToken.
func DecodeAccessToken(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
