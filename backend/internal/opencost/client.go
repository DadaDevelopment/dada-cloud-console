// Package opencost is a lean, read-only client for the OpenCost Allocation API
// (opencost.io/docs/integrations/api). The console backend uses it to surface
// per-project resource cost, aggregated by Kubernetes namespace.
//
// It mirrors the shape of internal/prometheus: New returns nil when the base
// URL is empty so callers can treat the cost feature as disabled. Only the one
// endpoint the UI needs -- GET /allocation/compute -- is implemented.
//
// OpenCost prices are configured on-cluster (custom RUB/hr pricing that mirrors
// the console billing engine), so the numeric costs returned here are already
// in roubles; the currency label is a presentation concern owned by the caller.
package opencost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a read-only OpenCost Allocation API client.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New creates a read-only OpenCost client. Returns nil when baseURL is empty so
// callers can treat the cost feature as disabled. baseURL is the API root
// (e.g. http://opencost.opencost.svc.cluster.local:9003), not an /allocation path.
func New(baseURL string) *Client {
	if baseURL == "" {
		return nil
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

// Allocation is one aggregated cost bucket from the Allocation API. OpenCost
// returns costs in the cluster's configured currency (RUB here). Only the
// fields the cost view needs are decoded.
type Allocation struct {
	Name        string     `json:"name"`
	CPUCost     float64    `json:"cpuCost"`
	RAMCost     float64    `json:"ramCost"`
	PVCost      float64    `json:"pvCost"`
	GPUCost     float64    `json:"gpuCost"`
	NetworkCost float64    `json:"networkCost"`
	TotalCost   float64    `json:"totalCost"`
	Properties  Properties `json:"properties"`
}

// Properties carries the identifying metadata OpenCost attaches to an
// allocation. Only the fields the cost attribution needs are decoded.
type Properties struct {
	Namespace string            `json:"namespace"`
	Pod       string            `json:"pod"`
	Labels    map[string]string `json:"labels"`
}

// computeEnvelope is the /allocation/compute response wrapper. data is an array
// of allocation sets (one per step in the window); with accumulate=true it
// collapses to a single set keyed by aggregation name.
type computeEnvelope struct {
	Code int                     `json:"code"`
	Data []map[string]Allocation `json:"data"`
}

// Compute calls GET /allocation/compute for the given window, aggregated by the
// given dimension (e.g. "namespace", or "namespace,label:dada_io_app"),
// accumulated into a single set. It returns the map keyed by aggregation name.
// OpenCost injects synthetic keys __idle__, __unallocated__ and __unmounted__;
// callers filter those out as needed.
//
// window accepts OpenCost duration/date forms: "7d", "30d", "24h", or an
// RFC3339 range "start,end". filter is an optional OpenCost filter expression
// (e.g. `namespace:"a","b"`); pass "" for no filter.
func (c *Client) Compute(ctx context.Context, window, aggregate, filter string) (map[string]Allocation, error) {
	q := url.Values{}
	q.Set("window", window)
	q.Set("aggregate", aggregate)
	q.Set("accumulate", "true")
	if filter != "" {
		q.Set("filter", filter)
	}
	u := c.baseURL + "/allocation/compute?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencost GET /allocation/compute: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("opencost GET /allocation/compute: status %d: %s", resp.StatusCode, string(body))
	}
	var env computeEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("opencost decode: %w", err)
	}
	if len(env.Data) == 0 {
		return map[string]Allocation{}, nil
	}
	return env.Data[0], nil
}
