// Package beget is a minimal client for the Beget Cloud REST API, used by the
// reverse-sync reader to discover VMs created outside the console.
//
// The Beget Terraform provider exposes no list data sources, so discovery must
// go through the REST API: VPS live under /v1/vps/server/list (S3 buckets, not
// handled here, live under /v1/cloud).
package beget

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client talks to the Beget Cloud REST API with a bearer token.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New constructs a Beget client. baseURL is e.g. "https://api.beget.com".
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// VPSConfiguration is the hardware/region slice of a VPS we care about.
type VPSConfiguration struct {
	CPUCount int    `json:"cpu_count"`
	Memory   int    `json:"memory"`    // MB
	DiskSize int    `json:"disk_size"` // MB
	Region   string `json:"region"`
}

// VPS is the subset of a /v1/vps/server/list entry the reader uses.
type VPS struct {
	ID            string           `json:"id"`   // stable Beget UUID (the import id)
	Slug          string           `json:"slug"` // stable machine name, e.g. "test-vm-01"
	DisplayName   string           `json:"display_name"`
	IPAddress     string           `json:"ip_address"`
	Status        string           `json:"status"`
	DateCreate    time.Time        `json:"date_create"`
	Configuration VPSConfiguration `json:"configuration"`
}

// ListVPS returns all VPS in the account.
func (c *Client) ListVPS(ctx context.Context) ([]VPS, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/vps/server/list", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("beget list vps: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("beget list vps: status %d: %s", resp.StatusCode, string(body))
	}

	var out struct {
		VPS []VPS `json:"vps"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode vps list: %w", err)
	}
	return out.VPS, nil
}
