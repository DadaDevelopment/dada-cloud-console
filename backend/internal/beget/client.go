// Package beget is a lean, read-only client for the Beget managed-Kubernetes
// billing surface (api.beget.com), used by the god-admin cost drilldown
// (admin_costs.go) to source the real hardware bill instead of the
// operator-typed HARDWARE_MONTHLY_COST_RUB fallback.
//
// Only the one endpoint the cost drilldown needs is implemented: GET
// /v1/k8s/cluster, which lists every cluster the token can see with its
// master node and worker-group pricing already computed by Beget
// (price_month, RUB). There is no dedicated "get cluster by slug" path in
// practice -- the list endpoint is cheap and cached daily by the caller, so
// filtering client-side is simpler than chasing an ID-scoped route.
//
// Mirrors the shape of internal/opencost: New returns nil when the token is
// empty so callers can treat the feature as disabled, and every response is
// read-only (the token this ships with is scoped to the existing Terraform
// bootstrap credential and must never be used for write calls).
package beget

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.beget.com"

// Client is a read-only Beget k8s billing client.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// New creates a read-only Beget client with a 15s timeout. Returns nil when
// token is empty so callers can treat the feature as disabled.
func New(token string) *Client {
	if token == "" {
		return nil
	}
	return &Client{
		baseURL:    defaultBaseURL,
		token:      token,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// WorkerGroup is one node pool inside a cluster, with Beget's own computed
// monthly price for every node in the group combined.
type WorkerGroup struct {
	DisplayName string  `json:"display_name"`
	NodeCount   int     `json:"node_count"`
	PriceMonth  float64 `json:"price_month"`
}

// Cluster is one managed Kubernetes cluster visible to the token.
type Cluster struct {
	ID              string  `json:"id"`
	Slug            string  `json:"slug"`
	DisplayName     string  `json:"display_name"`
	Status          string  `json:"status"`
	MasterPriceRUB  float64 `json:"master_price_month_rub"`
	MasterNodeCount int     `json:"master_node_count"`
	WorkerGroups    []WorkerGroup
}

// TotalMonthlyRUB sums the master control-plane price and every worker
// group's price into the cluster's real monthly hardware bill.
func (cl Cluster) TotalMonthlyRUB() float64 {
	total := cl.MasterPriceRUB
	for _, wg := range cl.WorkerGroups {
		total += wg.PriceMonth
	}
	return total
}

// apiError is the envelope Beget returns for domain errors. The API can
// answer HTTP 200 with an error body (observed on cluster/{id}-scoped
// routes hit without a valid ID), so every decode checks for it regardless
// of status code.
type apiError struct {
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type clusterListResponse struct {
	apiError
	ClusterInfo []struct {
		ID                string  `json:"id"`
		Slug              string  `json:"slug"`
		DisplayName       string  `json:"display_name"`
		Status            string  `json:"status"`
		PriceMonth        float64 `json:"price_month"`
		ConfigurationInfo struct {
			MasterNodeCount int `json:"master_node_count"`
		} `json:"configuration_info"`
		WorkerGroup []struct {
			PriceMonth         float64 `json:"price_month"`
			GroupConfiguration struct {
				DisplayName string `json:"display_name"`
				NodeCount   int    `json:"node_count"`
			} `json:"group_configuration"`
		} `json:"worker_group"`
	} `json:"cluster_info"`
}

// ListClusters returns every managed Kubernetes cluster visible to the
// token, master + worker-group pricing included.
func (c *Client) ListClusters(ctx context.Context) ([]Cluster, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/k8s/cluster", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("beget GET /v1/k8s/cluster: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("beget GET /v1/k8s/cluster: status %d: %s", resp.StatusCode, string(body))
	}

	var env clusterListResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("beget decode: %w", err)
	}
	if env.Error != nil {
		return nil, fmt.Errorf("beget GET /v1/k8s/cluster: %s: %s", env.Error.Code, env.Error.Message)
	}

	out := make([]Cluster, 0, len(env.ClusterInfo))
	for _, ci := range env.ClusterInfo {
		cl := Cluster{
			ID:              ci.ID,
			Slug:            ci.Slug,
			DisplayName:     ci.DisplayName,
			Status:          ci.Status,
			MasterPriceRUB:  ci.PriceMonth,
			MasterNodeCount: ci.ConfigurationInfo.MasterNodeCount,
		}
		for _, wg := range ci.WorkerGroup {
			cl.WorkerGroups = append(cl.WorkerGroups, WorkerGroup{
				DisplayName: wg.GroupConfiguration.DisplayName,
				NodeCount:   wg.GroupConfiguration.NodeCount,
				PriceMonth:  wg.PriceMonth,
			})
		}
		out = append(out, cl)
	}
	return out, nil
}

// SelectClusters filters clusters down to the ones named in slugCSV, a
// comma-separated list of slugs (case-insensitive, whitespace-tolerant). An
// empty/blank slugCSV selects every cluster the token can see -- the platform
// runs on more than one Beget-managed cluster, so "sum all of them" is the
// useful default for the hardware-cost drilldown.
func SelectClusters(clusters []Cluster, slugCSV string) []Cluster {
	if strings.TrimSpace(slugCSV) == "" {
		return clusters
	}
	want := map[string]bool{}
	for _, s := range strings.Split(slugCSV, ",") {
		if s = strings.TrimSpace(s); s != "" {
			want[strings.ToLower(s)] = true
		}
	}
	out := make([]Cluster, 0, len(clusters))
	for _, cl := range clusters {
		if want[strings.ToLower(cl.Slug)] {
			out = append(out, cl)
		}
	}
	return out
}
