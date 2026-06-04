// Package logsearch is a lean, read-only Elasticsearch client used by the
// console backend to read back the container logs that the portainer-agent's
// filebeat sidecars ship from VMs.
//
// It mirrors internal/portainer / internal/prometheus: New returns nil when
// unconfigured so callers treat aggregated log search as disabled. Only a
// single bounded _search is implemented.
//
// NOTE: the _source field names (message / host.name / container label) and the
// default index pattern are assumptions about the filebeat mapping. They are
// intentionally isolated in this file (sourceDoc + buildQuery) so adjusting them
// to a live cluster is a one-place change.
package logsearch

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

// Client is a read-only Elasticsearch search client.
type Client struct {
	baseURL    string
	index      string
	apiKey     string
	httpClient *http.Client
}

// New creates a read-only ES client. Returns nil when baseURL is empty so
// callers can treat log search as disabled. index defaults to "dada-vm-logs-*"
// (the index the VM filebeat bootstrap writes to:
// dada-vm-logs-<app|unknown>-<date>).
func New(baseURL, apiKey, index string) *Client {
	if baseURL == "" {
		return nil
	}
	if index == "" {
		index = "dada-vm-logs-*"
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		index:      index,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// SearchOpts bounds an aggregated log search. At least one of VMName/App should
// be set by callers for project-scoping (enforced at the handler layer).
type SearchOpts struct {
	VMName string
	App    string
	Query  string // free text → ES query_string over the message field
	Since  time.Time
	Until  time.Time
	Size   int // default 200, cap 1000
}

// LogEntry is one normalized log line.
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Message   string `json:"message"`
	VMName    string `json:"vm_name,omitempty"`
	App       string `json:"app,omitempty"`
	Stream    string `json:"stream,omitempty"`
}

// SearchResult is the normalized search response.
type SearchResult struct {
	Total   int        `json:"total"`
	Entries []LogEntry `json:"entries"`
}

// sourceDoc maps the filebeat _source fields we care about. Adjust here if the
// live mapping differs.
type sourceDoc struct {
	Timestamp string `json:"@timestamp"`
	Message   string `json:"message"`
	VMName    string `json:"vm_name"`
	App       string `json:"app"`
	Host      struct {
		Name string `json:"name"`
	} `json:"host"`
	Stream string `json:"stream"`
}

func (c *Client) buildQuery(opts SearchOpts) map[string]any {
	filters := []map[string]any{}
	if opts.VMName != "" {
		// vm_name is the prometheus-agent external label; host.name is filebeat's
		// default. Match either so we don't depend on one mapping choice.
		filters = append(filters, map[string]any{
			"bool": map[string]any{
				"should": []map[string]any{
					{"term": map[string]any{"vm_name": opts.VMName}},
					{"term": map[string]any{"host.name": opts.VMName}},
				},
				"minimum_should_match": 1,
			},
		})
	}
	if opts.App != "" {
		filters = append(filters, map[string]any{
			"bool": map[string]any{
				"should": []map[string]any{
					{"term": map[string]any{"app": opts.App}},
					{"match": map[string]any{"container.labels.dada_io_app": opts.App}},
				},
				"minimum_should_match": 1,
			},
		})
	}
	rng := map[string]any{}
	if !opts.Since.IsZero() {
		rng["gte"] = opts.Since.UTC().Format(time.RFC3339)
	}
	if !opts.Until.IsZero() {
		rng["lte"] = opts.Until.UTC().Format(time.RFC3339)
	}
	if len(rng) > 0 {
		filters = append(filters, map[string]any{"range": map[string]any{"@timestamp": rng}})
	}

	must := []map[string]any{}
	if opts.Query != "" {
		must = append(must, map[string]any{
			"query_string": map[string]any{
				"query":         opts.Query,
				"default_field": "message",
				"lenient":       true,
			},
		})
	}

	size := opts.Size
	if size <= 0 {
		size = 200
	}
	if size > 1000 {
		size = 1000
	}

	return map[string]any{
		"size": size,
		"sort": []map[string]any{{"@timestamp": map[string]any{"order": "desc"}}},
		"query": map[string]any{
			"bool": map[string]any{
				"filter": filters,
				"must":   must,
			},
		},
	}
}

// Search runs a bounded _search against the configured index and returns
// normalized, newest-first log entries.
func (c *Client) Search(ctx context.Context, opts SearchOpts) (*SearchResult, error) {
	body, err := json.Marshal(c.buildQuery(opts))
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/%s/_search", c.baseURL, c.index)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "ApiKey "+c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch search: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // cap 8 MiB
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("elasticsearch search: status %d: %s", resp.StatusCode, string(raw))
	}

	var parsed struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []struct {
				Source sourceDoc `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("elasticsearch decode: %w", err)
	}

	out := &SearchResult{Total: parsed.Hits.Total.Value}
	for _, h := range parsed.Hits.Hits {
		vm := h.Source.VMName
		if vm == "" {
			vm = h.Source.Host.Name
		}
		out.Entries = append(out.Entries, LogEntry{
			Timestamp: h.Source.Timestamp,
			Message:   h.Source.Message,
			VMName:    vm,
			App:       h.Source.App,
			Stream:    h.Source.Stream,
		})
	}
	return out, nil
}
