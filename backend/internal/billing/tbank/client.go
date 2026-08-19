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
	"strings"
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

// StatementOperation is one line of an account statement, normalized from
// the wire shape T-Bank actually returns: an amount in account currency,
// the payer's own free-text payment purpose (where the invoice number is
// expected to appear), and the direction of the money.
type StatementOperation struct {
	OperationID string
	// Type is "Credit" for money arriving on the account and "Debit" for
	// money leaving it. Only a Credit can settle an invoice.
	Type string
	// Status is "Transaction" for a settled operation. A hold or an
	// authorization is not money that has landed.
	Status       string
	Amount       float64
	CurrencyCode string
	Purpose      string
	Date         string
	PayerINN     string
	PayerName    string
}

// IsSettledCredit reports whether the operation is money that has actually
// arrived on the account. Reconciliation must never settle an invoice from
// a debit (our own outgoing payment) or from an unsettled hold.
func (o StatementOperation) IsSettledCredit() bool {
	return strings.EqualFold(o.Type, "Credit") && strings.EqualFold(o.Status, "Transaction")
}

// statementParty is the payer/receiver block T-Bank attaches to an
// operation. Only the payer's identity is read, to record who paid.
type statementParty struct {
	INN  string `json:"inn"`
	Name string `json:"name"`
}

// wireOperation is the raw statement line as T-Bank sends it. The API
// returns amounts as JSON numbers (not strings) and carries the payment
// purpose in payPurpose -- description holds only a short bank-side label.
type wireOperation struct {
	OperationID     string         `json:"operationId"`
	TypeOfOperation string         `json:"typeOfOperation"`
	Status          string         `json:"operationStatus"`
	AccountAmount   float64        `json:"accountAmount"`
	RubleAmount     float64        `json:"rubleAmount"`
	CurrencyCode    string         `json:"accountCurrencyDigitalCode"`
	PayPurpose      string         `json:"payPurpose"`
	Description     string         `json:"description"`
	OperationDate   string         `json:"operationDate"`
	Payer           statementParty `json:"payer"`
}

// normalize maps a wire operation onto StatementOperation. Purpose falls
// back to description only when payPurpose is absent, so a bank-side label
// never masquerades as the payer's own text.
func (w wireOperation) normalize() StatementOperation {
	amount := w.AccountAmount
	if amount == 0 {
		amount = w.RubleAmount
	}
	purpose := w.PayPurpose
	if purpose == "" {
		purpose = w.Description
	}
	return StatementOperation{
		OperationID:  w.OperationID,
		Type:         w.TypeOfOperation,
		Status:       w.Status,
		Amount:       amount,
		CurrencyCode: w.CurrencyCode,
		Purpose:      purpose,
		Date:         w.OperationDate,
		PayerINN:     w.Payer.INN,
		PayerName:    w.Payer.Name,
	}
}

// statementResponse is the subset of GET /statement this client reads.
type statementResponse struct {
	Operations []wireOperation `json:"operations"`
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
// (inclusive), both sent as full RFC3339 timestamps in UTC. The T-Bank
// Business API rejects a bare YYYY-MM-DD date on these parameters.
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
	ops := make([]StatementOperation, 0, len(body.Operations))
	for _, w := range body.Operations {
		ops = append(ops, w.normalize())
	}
	return ops, nil
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
