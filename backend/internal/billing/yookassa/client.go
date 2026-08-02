// Package yookassa is a thin client for the YooKassa v3 Payments API
// (https://yookassa.ru/developers/api). It covers creating a payment — both
// the redirect-confirmation kind a customer completes in a browser and the
// recurring kind charged against a previously saved payment method — and
// re-fetching a payment by id. Webhooks carry no signature, so callers must
// always re-fetch via GetPayment before trusting a webhook payload.
package yookassa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// defaultBaseURL is the production YooKassa API root.
const defaultBaseURL = "https://api.yookassa.ru/v3"

// Client is a YooKassa v3 Payments API client. Zero value is not usable; use
// New. BaseURL and HTTPClient are exported so tests can point the client at
// an httptest.Server and a client with a short timeout.
type Client struct {
	ShopID     string
	SecretKey  string
	BaseURL    string
	HTTPClient *http.Client
}

// New builds a Client against the production YooKassa API. The HTTP client
// carries its own timeout so a hung YooKassa endpoint cannot pin a webhook
// or checkout goroutine beyond it even when the caller's context has no
// deadline.
func New(shopID, secretKey string) *Client {
	return &Client{
		ShopID:     shopID,
		SecretKey:  secretKey,
		BaseURL:    defaultBaseURL,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Amount is a monetary amount as YooKassa expects it: a decimal string value
// (e.g. "990.00") plus an ISO 4217 currency code.
type Amount struct {
	Value    string `json:"value"`
	Currency string `json:"currency"`
}

// Confirmation describes how the payer confirms the payment. Slice 1 only
// uses the "redirect" type.
type Confirmation struct {
	Type      string `json:"type"`
	ReturnURL string `json:"return_url,omitempty"`
	URL       string `json:"confirmation_url,omitempty"`
}

// ReceiptCustomer identifies the payer for 54-FZ fiscalization.
type ReceiptCustomer struct {
	Email string `json:"email,omitempty"`
}

// ReceiptItem is one fiscal receipt line item.
type ReceiptItem struct {
	Description    string `json:"description"`
	Quantity       string `json:"quantity"`
	Amount         Amount `json:"amount"`
	VatCode        int    `json:"vat_code"`
	PaymentMode    string `json:"payment_mode"`
	PaymentSubject string `json:"payment_subject"`
}

// Receipt is the optional 54-FZ fiscal receipt block, included in the create
// request only when the shop has fiscalization enabled. TaxSystemCode is
// required only for merchants registered under more than one tax system;
// sending it when the shop has a single one is rejected, hence omitempty.
type Receipt struct {
	Customer      ReceiptCustomer `json:"customer"`
	Items         []ReceiptItem   `json:"items"`
	TaxSystemCode int             `json:"tax_system_code,omitempty"`
}

// CreatePaymentRequest is the body of POST /v3/payments. It covers both
// flows:
//
// The customer-present flow sets Confirmation (redirect) and, when the
// customer consented to auto-renewal, SavePaymentMethod — YooKassa then
// returns a reusable payment_method on the succeeded payment.
//
// The recurring flow sets PaymentMethodID to that saved id and omits
// Confirmation entirely: nobody is at the keyboard to confirm anything, and
// sending a confirmation block with a payment_method_id is an API error.
type CreatePaymentRequest struct {
	Amount            Amount         `json:"amount"`
	Capture           bool           `json:"capture"`
	Confirmation      *Confirmation  `json:"confirmation,omitempty"`
	Description       string         `json:"description,omitempty"`
	Receipt           *Receipt       `json:"receipt,omitempty"`
	SavePaymentMethod bool           `json:"save_payment_method,omitempty"`
	PaymentMethodID   string         `json:"payment_method_id,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
}

// PaymentMethod is the payment instrument YooKassa used. Saved reports
// whether it may be charged again without the customer present; when it is
// true, ID is the handle to store and send as PaymentMethodID later. Title is
// a display string such as "Bank card *4444", shown in the console so the
// customer knows what will be charged.
type PaymentMethod struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Saved bool   `json:"saved"`
	Title string `json:"title"`
}

// Payment is the subset of the YooKassa payment object this client reads back.
type Payment struct {
	ID            string            `json:"id"`
	Status        string            `json:"status"`
	Paid          bool              `json:"paid"`
	Amount        Amount            `json:"amount"`
	Confirmation  Confirmation      `json:"confirmation"`
	PaymentMethod PaymentMethod     `json:"payment_method"`
	Metadata      map[string]string `json:"metadata"`
}

// apiError is the YooKassa error envelope returned on non-2xx responses.
type apiError struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Code        string `json:"code"`
	Description string `json:"description"`
}

// Error is returned by CreatePayment/GetPayment for a non-2xx YooKassa
// response. StatusCode is the HTTP status; Code/Description come from the
// YooKassa error envelope when present.
type Error struct {
	StatusCode  int
	Code        string
	Description string
}

func (e *Error) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("yookassa: %d %s: %s", e.StatusCode, e.Code, e.Description)
	}
	return fmt.Sprintf("yookassa: unexpected status %d", e.StatusCode)
}

// CreatePayment creates a new payment. idempotenceKey is sent as the
// Idempotence-Key header and must be a UUID unique to this logical create
// (YooKassa treats a retried request with the same key as the same payment).
func (c *Client) CreatePayment(ctx context.Context, idempotenceKey string, req CreatePaymentRequest) (Payment, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return Payment{}, fmt.Errorf("yookassa: marshal create request: %w", err)
	}
	return c.do(ctx, http.MethodPost, "/payments", idempotenceKey, body)
}

// GetPayment re-fetches a payment by id. This is the authoritative status
// read — webhook payloads carry no signature and must never be trusted
// without this call.
func (c *Client) GetPayment(ctx context.Context, id string) (Payment, error) {
	return c.do(ctx, http.MethodGet, "/payments/"+id, "", nil)
}

func (c *Client) do(ctx context.Context, method, path, idempotenceKey string, body []byte) (Payment, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, c.baseURL()+path, bodyReader)
	if err != nil {
		return Payment{}, fmt.Errorf("yookassa: build request: %w", err)
	}
	httpReq.SetBasicAuth(c.ShopID, c.SecretKey)
	httpReq.Header.Set("Content-Type", "application/json")
	if idempotenceKey != "" {
		httpReq.Header.Set("Idempotence-Key", idempotenceKey)
	}

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return Payment{}, fmt.Errorf("yookassa: request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Payment{}, fmt.Errorf("yookassa: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := Error{StatusCode: resp.StatusCode}
		var envelope apiError
		if json.Unmarshal(respBody, &envelope) == nil {
			apiErr.Code = envelope.Code
			apiErr.Description = envelope.Description
		}
		return Payment{}, &apiErr
	}

	var payment Payment
	if err := json.Unmarshal(respBody, &payment); err != nil {
		return Payment{}, fmt.Errorf("yookassa: decode response: %w", err)
	}
	return payment, nil
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return defaultBaseURL
}
