package fincore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	fcapi "github.com/dada-tuda/console/backend/internal/fincore/client"
)

const (
	clientsUpsertPath = "/api/ingest/clients/upsert"
	ingestPath        = "/api/ingest/transactions"
	tenantHeader      = fcapi.TenantHeader
	tokenPrefix       = "fcs_"

	// maxBatch is IngestTransactionsIn.items' own max_length. A larger slice is
	// split rather than rejected.
	maxBatch = 500

	defaultTimeout = 60 * time.Second
)

// Client talks to one FinCore tenant over the machine ingest seam.
//
// Transport, paths and response decoding come from the vendored generated SDK
// (internal/fincore/client, regenerated from FinCore's openapi.json), so a
// contract change shows up as a compile error instead of a runtime 422.
//
// The token must be a FinCore service token (fcs_...): the ingest router
// authenticates through require_scopes, which refuses a human's JWT outright.
// The tenant slug is sent explicitly even though the token is already bound to
// a tenant, because the resolver treats the header as the request's answer.
type Client struct {
	BaseURL    string
	Token      string
	TenantSlug string

	api *fcapi.ClientWithResponses
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
	api, err := fcapi.NewFinCoreClient(baseURL, token, tenantSlug,
		fcapi.WithHTTPClient(&http.Client{Timeout: defaultTimeout}))
	if err != nil {
		return nil
	}
	return &Client{
		BaseURL:    baseURL,
		Token:      token,
		TenantSlug: tenantSlug,
		api:        api,
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
	for _, chunk := range chunkBatch(items) {
		body, err := encodeBatch(chunk, clientsUpsertPath)
		if err != nil {
			return total, err
		}
		resp, err := c.api.IngestClientsUpsertWithBodyWithResponse(ctx, contentTypeJSON, body)
		if err != nil {
			return total, fmt.Errorf("fincore: %s: %w", clientsUpsertPath, err)
		}
		out, err := batchOutcome(clientsUpsertPath, resp.HTTPResponse, resp.Body, resp.JSON200)
		if err != nil {
			return total, err
		}
		total.Received += out.Received
		total.Created += out.Created
		total.Updated += out.Updated
		for _, r := range out.Results {
			total.Results = append(total.Results, ClientResult{
				ExternalID: r.ExternalId,
				ClientID:   int64(r.ClientId),
				Created:    r.Created,
			})
		}
	}
	return total, nil
}

// IngestTransactions pushes money facts. Repeating a call with the same
// source identities converges on the same rows instead of booking the money
// twice, so a failed run is retried by simply running it again.
//
// The batch is encoded here rather than through the SDK's typed
// IngestTransactionsWithResponse: the generated IngestTransactionIn declares
// operation_date as time.Time, which marshals with a zone offset, and FinCore
// stores it in a TIMESTAMP WITHOUT TIME ZONE column -- asyncpg rejects the
// value and the whole batch returns "http 503: database_unavailable". Our
// Transaction sends WallTime instead; everything else on the request -- base
// URL, path, headers, response decoding -- is the SDK's.
func (c *Client) IngestTransactions(ctx context.Context, items []Transaction) (IngestResult, error) {
	var total IngestResult
	for _, chunk := range chunkBatch(items) {
		body, err := encodeBatch(chunk, ingestPath)
		if err != nil {
			return total, err
		}
		resp, err := c.api.IngestTransactionsWithBodyWithResponse(ctx, contentTypeJSON, body)
		if err != nil {
			return total, fmt.Errorf("fincore: %s: %w", ingestPath, err)
		}
		out, err := batchOutcome(ingestPath, resp.HTTPResponse, resp.Body, resp.JSON200)
		if err != nil {
			return total, err
		}
		total.Received += out.Received
		total.Created += out.Created
		total.Updated += out.Updated
		total.Unchanged += out.Unchanged
		for _, r := range out.Results {
			total.Results = append(total.Results, TransactionResult{
				SourceIdentity:       r.SourceIdentity,
				StatementID:          int64(r.StatementId),
				FactID:               optionalInt64(r.FactId),
				Status:               string(r.Status),
				ClientID:             optionalInt64(r.ClientId),
				ProjectID:            optionalInt64(r.ProjectId),
				ClassificationStatus: optionalString(r.ClassificationStatus),
			})
		}
	}
	return total, nil
}

const contentTypeJSON = "application/json"

// chunkBatch splits a slice into pieces the endpoint's max_length accepts.
func chunkBatch[T any](items []T) [][]T {
	var out [][]T
	for start := 0; start < len(items); start += maxBatch {
		end := start + maxBatch
		if end > len(items) {
			end = len(items)
		}
		out = append(out, items[start:end])
	}
	return out
}

// encodeBatch renders the {source_system, items} envelope both ingest
// endpoints declare.
func encodeBatch[T any](items []T, path string) (*bytes.Reader, error) {
	payload, err := json.Marshal(map[string]any{"source_system": SourceSystem, "items": items})
	if err != nil {
		return nil, fmt.Errorf("fincore: encode %s: %w", path, err)
	}
	return bytes.NewReader(payload), nil
}

// batchOutcome returns the decoded answer for a batch call.
//
// The SDK only fills its typed JSON200 field when the response carries a JSON
// content type; a 2xx whose body is JSON but whose header is missing is still a
// success, so it is decoded here rather than reported as a failure.
func batchOutcome[T any](path string, resp *http.Response, body []byte, parsed *T) (*T, error) {
	if parsed != nil {
		return parsed, nil
	}
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	if status < 200 || status >= 300 {
		return nil, httpFailure(path, resp, body)
	}
	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("fincore: decode %s: %w", path, err)
	}
	return &out, nil
}

// httpFailure names the endpoint, the status and the server's own reason --
// FinCore answers a refused scope or a rejected item with a readable detail.
func httpFailure(path string, resp *http.Response, body []byte) error {
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	return fmt.Errorf("fincore: %s: http %d: %s", path, status, strings.TrimSpace(string(body)))
}

// optionalString flattens the SDK's nullable string: absent and null both
// read as an empty status.
func optionalString(v interface{ Get() (string, error) }) string {
	got, err := v.Get()
	if err != nil {
		return ""
	}
	return got
}

// optionalInt64 turns the SDK's nullable int into the pointer our result type
// exposes: absent and null both read as "FinCore did not settle on one".
func optionalInt64(v interface{ Get() (int, error) }) *int64 {
	got, err := v.Get()
	if err != nil {
		return nil
	}
	out := int64(got)
	return &out
}
