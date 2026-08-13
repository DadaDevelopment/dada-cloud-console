// Package apiclient is a minimal client for the console's HTTP API, used by
// the ddc CLI. It carries the caller's bearer token and marks every request
// with X-Dada-Client (and, when detected, an agent-session marker) so the
// server's audit trail can distinguish CLI-originated deploys from the web
// console.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"time"

	"github.com/dada-tuda/console/cli/internal/version"
)

// ClientHeaderName is sent with every request this package makes, so the
// server can attribute the request to the ddc CLI (as opposed to the web
// console or a raw curl call) regardless of auth method.
const ClientHeaderName = "X-Dada-Client"

// AgentMarkerHeaderName is sent only when an agent-session environment
// variable is detected (see internal/agentmarker). Its value is the name of
// the environment variable that was found, e.g. "CLAUDECODE" - not a guess at
// which product it belongs to. The name must stay in step with the header the
// console reads in clientClaimMiddleware (backend/internal/api/audit.go): a
// marker sent under any other name is silently dropped, and the "0 agentic
// calls" half of the CLI kill-criterion would then be unfalsifiable.
const AgentMarkerHeaderName = "X-Dada-Agent-Session"

// TokenSource returns a valid bearer access token, refreshing it if
// necessary. It returns ErrNotLoggedIn when no token is available.
type TokenSource func(ctx context.Context) (string, error)

// Client talks to the console API.
type Client struct {
	BaseURL     string
	HTTP        *http.Client
	Token       TokenSource
	AgentMarker string
}

// New builds a Client. baseURL should have no trailing slash, e.g.
// "https://console.dada-tuda.ru/api/v1".
func New(baseURL string, hc *http.Client, token TokenSource, agentMarker string) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{BaseURL: baseURL, HTTP: hc, Token: token, AgentMarker: agentMarker}
}

// APIError is a non-2xx response from the console API, carrying the HTTP
// status and, when the server sent one, a machine-readable code. Callers
// must branch on Status/Code, never on Message text.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("request failed with status %d", e.Status)
}

type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set(ClientHeaderName, "cli/"+version.Version)
	if c.AgentMarker != "" {
		req.Header.Set(AgentMarkerHeaderName, c.AgentMarker)
	}
	if c.Token != nil {
		tok, err := c.Token(ctx)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req, nil
}

// doJSON performs a request and decodes a JSON response into out (skipped
// when out is nil), returning *APIError for any non-2xx status.
func (c *Client) doJSON(ctx context.Context, method, path string, body io.Reader, contentType string, out any) error {
	var payload []byte
	if body != nil {
		var err error
		payload, err = io.ReadAll(body)
		if err != nil {
			return err
		}
	}

	resp, err := c.send(ctx, method, path, payload, contentType)
	if err != nil {
		return fmt.Errorf("contacting console: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading console response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var eb errorBody
		_ = json.Unmarshal(data, &eb)
		return &APIError{Status: resp.StatusCode, Code: eb.Code, Message: eb.Error}
	}

	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("console sent an unreadable response: %w", err)
	}
	return nil
}

// transportRetries is how many extra attempts send makes after a connection
// that died before the request was on the wire. Three attempts with the
// backoff below cover the seconds-long TLS handshake stalls users hit on flaky
// networks without making a broken console feel like a hang.
const transportRetries = 2

var retryBackoff = []time.Duration{400 * time.Millisecond, 1200 * time.Millisecond}

// send performs the request, retrying transport failures that happened before
// the request was written. That distinction is what makes retrying a POST
// safe: if the bytes never left the client, the console cannot have acted on
// them, so a second attempt cannot duplicate a deploy or a build.
func (c *Client) send(ctx context.Context, method, path string, payload []byte, contentType string) (*http.Response, error) {
	var lastErr error
	for attempt := 0; ; attempt++ {
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := c.newRequest(ctx, method, path, body, contentType)
		if err != nil {
			return nil, err
		}

		var wrote bool
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
			WroteRequest: func(httptrace.WroteRequestInfo) { wrote = true },
		}))

		resp, err := c.HTTP.Do(req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if wrote || attempt >= transportRetries || ctx.Err() != nil {
			return nil, lastErr
		}
		select {
		case <-ctx.Done():
			return nil, lastErr
		case <-time.After(retryBackoff[attempt]):
		}
	}
}

func jsonBody(v any) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(v); err != nil {
		return nil, err
	}
	return buf, nil
}

// Explain turns an error from this package into a message meant for a
// terminal, branching on APIError.Status/Code rather than any prose the
// server returned - the same rule the rest of this codebase follows after
// getting burned by matching on error text.
func Explain(err error) string {
	var ae *APIError
	if !asAPIError(err, &ae) {
		return err.Error()
	}
	switch ae.Status {
	case http.StatusUnauthorized:
		return "вы не вошли (или сессия истекла) - выполните 'ddc login'"
	case http.StatusForbidden:
		return "нет прав на запись в этот проект/окружение"
	case http.StatusNotFound:
		return "проект, окружение или приложение не найдены"
	case http.StatusRequestEntityTooLarge:
		return "архив больше лимита загрузки консоли в 100МБ"
	case http.StatusServiceUnavailable:
		return "загрузка исходников сейчас выключена на этой консоли"
	default:
		if ae.Message != "" {
			return ae.Message
		}
		return fmt.Sprintf("консоль вернула статус %d", ae.Status)
	}
}

func asAPIError(err error, target **APIError) bool {
	ae, ok := err.(*APIError)
	if !ok {
		return false
	}
	*target = ae
	return true
}
