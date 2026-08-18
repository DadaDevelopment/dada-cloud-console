// Package tbank is a thin client and reconciliation provider for T-Bank
// Business statements (https://business.tbank.ru/openapi/docs/openapi.yaml).
// It backs the invoice payment method: a payer transfers money by hand from
// their own bank, and this package periodically reads the platform's account
// statement and matches incoming operations back to pending invoice payments
// by invoice number and amount.
package tbank

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// defaultBaseURL is the production T-Bank Business OpenAPI root.
const defaultBaseURL = "https://business.tbank.ru/openapi/api/v1"

// sandboxBaseURL is the T-Bank Business sandbox root, used when
// cfg.TBankSandbox is set so integration can be exercised without moving
// real money.
const sandboxBaseURL = "https://business.tbank.ru/openapi/sandbox/api/v1"

// SandboxToken is the fixed token T-Bank documents for the sandbox
// environment (business.tbank.ru/openapi/docs, "Авторизация" section:
// "Bearer TBankSandboxToken"). It authenticates against sandboxBaseURL only
// and carries no access to a real account, so hardcoding it here is the same
// shape as any other public sandbox credential.
const SandboxToken = "TBankSandboxToken"

// Client is a T-Bank Business statement API client. Zero value is not
// usable; use New.
type Client struct {
	Token      string
	BaseURL    string
	HTTPClient *http.Client
}

// New builds a Client. sandbox selects the sandbox base URL and the fixed
// SandboxToken over the caller-supplied token, mirroring how a sandbox
// integration is meant to be exercised without a real credential.
func New(token string, sandbox bool) *Client {
	baseURL := defaultBaseURL
	if sandbox {
		baseURL = sandboxBaseURL
		token = SandboxToken
	}
	return &Client{
		Token:      token,
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// StatementOperation is one line of an account statement: money that moved,
// with the payer's own free-text payment purpose (where the invoice number
// is expected to appear) and an amount in rubles as a decimal string.
type StatementOperation struct {
	OperationID string `json:"operationId"`
	Amount      string `json:"amount"`
	Description string `json:"description"`
	Date        string `json:"date"`
}

// statementResponse is the subset of GET /statement this client reads.
type statementResponse struct {
	Operations []StatementOperation `json:"operations"`
}

// apiError is the T-Bank Business error envelope returned on non-2xx
// responses.
type apiError struct {
	Message string `json:"message"`
	Code    string `json:"errorCode"`
}

// Error is returned by Statement for a non-2xx T-Bank response.
type Error struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("tbank: %d %s: %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("tbank: unexpected status %d", e.StatusCode)
}

// Statement fetches every operation on accountNumber between from and to
// (inclusive), both formatted as YYYY-MM-DD per the T-Bank Business API.
func (c *Client) Statement(ctx context.Context, accountNumber string, from, to time.Time) ([]StatementOperation, error) {
	url := fmt.Sprintf("%s/statement?accountNumber=%s&from=%s&to=%s",
		c.baseURL(), accountNumber, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("tbank: build statement request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("tbank: statement request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tbank: read statement response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := Error{StatusCode: resp.StatusCode}
		var envelope apiError
		if json.Unmarshal(respBody, &envelope) == nil {
			apiErr.Code = envelope.Code
			apiErr.Message = envelope.Message
		}
		return nil, &apiErr
	}

	var body statementResponse
	if err := json.Unmarshal(respBody, &body); err != nil {
		return nil, fmt.Errorf("tbank: decode statement response: %w", err)
	}
	return body.Operations, nil
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return defaultBaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}
