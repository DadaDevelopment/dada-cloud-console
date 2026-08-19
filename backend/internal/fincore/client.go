package fincore

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

const (
	clientsUpsertPath = "/api/ingest/clients/upsert"
	ingestPath        = "/api/ingest/transactions"
	tenantHeader      = "x-tenant-slug"
	tokenPrefix       = "fcs_"

	// maxBatch is IngestTransactionsIn.items' own max_length. A larger slice is
	// split rather than rejected.
	maxBatch = 500

	defaultTimeout = 60 * time.Second
)

// Client talks to one FinCore tenant over the machine ingest seam.
//
// The token must be a FinCore service token (fcs_...): the ingest router
// authenticates through require_scopes, which refuses a human's JWT outright.
// The tenant slug is sent explicitly even though the token is already bound to
// a tenant, because the resolver treats the header as the request's answer.
type Client struct {
	BaseURL    string
	Token      string
	TenantSlug string
	HTTPClient *http.Client
}

// New builds a Client. It returns nil when the integration is not configured,
// mirroring internal/beget and internal/opencost: callers treat a nil client as
// "feature off" rather than branching on a separate flag.
func New(baseURL, token, tenantSlug string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	tenantSlug = strings.TrimSpace(tenantSlug)
	if baseURL == "" || token == "" || tenantSlug == "" {
		return nil
	}
	return &Client{
		BaseURL:    baseURL,
		Token:      token,
		TenantSlug: tenantSlug,
		HTTPClient: &http.Client{Timeout: defaultTimeout},
	}
}

// LooksLikeServiceToken reports whether the configured credential is the kind
// the ingest seam accepts. A personal JWT reaches the endpoint and is refused
// with 401, which is worth naming at startup instead of at the first sync.
func (c *Client) LooksLikeServiceToken() bool {
	return strings.HasPrefix(c.Token, tokenPrefix)
}

// UpsertClients syncs Dada Cloud users into FinCore clients by external key.
// Batches over the server limit are split; the returned result is the sum.
func (c *Client) UpsertClients(ctx context.Context, items []ClientUpsert) (ClientsUpsertResult, error) {
	var total ClientsUpsertResult
	for _, chunk := range chunkClients(items) {
		var out ClientsUpsertResult
		body := map[string]any{"source_system": SourceSystem, "items": chunk}
		if err := c.do(ctx, http.MethodPost, clientsUpsertPath, body, &out); err != nil {
			return total, err
		}
		total.Received += out.Received
		total.Created += out.Created
		total.Updated += out.Updated
		total.Results = append(total.Results, out.Results...)
	}
	return total, nil
}

// IngestTransactions pushes money facts. Repeating a call with the same
// source identities converges on the same rows instead of booking the money
// twice, so a failed run is retried by simply running it again.
func (c *Client) IngestTransactions(ctx context.Context, items []Transaction) (IngestResult, error) {
	var total IngestResult
	for _, chunk := range chunkTransactions(items) {
		var out IngestResult
		body := map[string]any{"source_system": SourceSystem, "items": chunk}
		if err := c.do(ctx, http.MethodPost, ingestPath, body, &out); err != nil {
			return total, err
		}
		total.Received += out.Received
		total.Created += out.Created
		total.Updated += out.Updated
		total.Unchanged += out.Unchanged
		total.Results = append(total.Results, out.Results...)
	}
	return total, nil
}

func chunkClients(items []ClientUpsert) [][]ClientUpsert {
	var out [][]ClientUpsert
	for start := 0; start < len(items); start += maxBatch {
		end := start + maxBatch
		if end > len(items) {
			end = len(items)
		}
		out = append(out, items[start:end])
	}
	return out
}

func chunkTransactions(items []Transaction) [][]Transaction {
	var out [][]Transaction
	for start := 0; start < len(items); start += maxBatch {
		end := start + maxBatch
		if end > len(items) {
			end = len(items)
		}
		out = append(out, items[start:end])
	}
	return out
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	payload, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("fincore: encode %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("fincore: build request %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set(tenantHeader, c.TenantSlug)
	req.Header.Set("Content-Type", "application/json")

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fincore: %s: %w", path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("fincore: read %s: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fincore: %s: http %d: %s", path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("fincore: decode %s: %w", path, err)
	}
	return nil
}
