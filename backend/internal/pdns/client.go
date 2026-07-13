// Package pdns is a small client for the PowerDNS authoritative HTTP API
// (/api/v1). The console backend uses it to manage customer zones for the
// NS-delegation ("managed DNS") path: it creates a zone delegated to the
// platform nameservers, seeds apex/www routing records, and exposes full
// record CRUD. PowerDNS is the source of truth for records -- we do not mirror
// them in our database, to avoid drift.
//
// All zone and record names are normalized to the FQDN-with-trailing-dot form
// PowerDNS requires. New returns nil when baseURL is empty so callers can treat
// the feature as disabled.
package pdns

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

// Client talks to a single PowerDNS API server (localhost server id). It carries
// the X-API-Key on every request and has no global state.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient builds a PowerDNS API client. baseURL is the API root
// (e.g. http://powerdns-api.powerdns.svc:8081), apiKey is the X-API-Key value.
// Returns nil when baseURL is empty so callers can treat managed DNS as disabled.
func NewClient(baseURL, apiKey string, httpTimeout time.Duration) *Client {
	if baseURL == "" {
		return nil
	}
	if httpTimeout <= 0 {
		httpTimeout = 15 * time.Second
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: httpTimeout},
	}
}

// Record is a single record content line within an RRSet.
type Record struct {
	Content  string `json:"content"`
	Disabled bool   `json:"disabled"`
}

// RRSet is a set of records sharing a name + type in a zone.
type RRSet struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	TTL        int      `json:"ttl"`
	ChangeType string   `json:"changetype,omitempty"`
	Records    []Record `json:"records,omitempty"`
}

// Zone is the subset of the PowerDNS zone object the console reads.
type Zone struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Kind   string  `json:"kind"`
	RRSets []RRSet `json:"rrsets"`
}

// APIError is a typed error carrying the PowerDNS HTTP status and body so
// callers can distinguish conflicts (zone already exists) from other failures.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("powerdns api: status %d: %s", e.StatusCode, e.Body)
}

// IsConflict reports whether err is an APIError with HTTP 409 (Conflict), i.e.
// the zone already exists.
func IsConflict(err error) bool {
	if ae, ok := err.(*APIError); ok {
		return ae.StatusCode == http.StatusConflict
	}
	return false
}

// IsNotFound reports whether err is an APIError with HTTP 404.
func IsNotFound(err error) bool {
	if ae, ok := err.(*APIError); ok {
		return ae.StatusCode == http.StatusNotFound
	}
	return false
}

// ensureDot returns name as a FQDN with exactly one trailing dot.
func ensureDot(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return name
	}
	if !strings.HasSuffix(name, ".") {
		return name + "."
	}
	return name
}

// zoneID returns the canonical zone id PowerDNS uses in the URL path (the apex
// with a trailing dot).
func zoneID(apex string) string {
	return ensureDot(apex)
}

func (c *Client) do(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(respBody))}
	}
	return respBody, nil
}

type createZoneRequest struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Nameservers []string `json:"nameservers"`
}

// CreateZone creates a Native zone for apex delegated to the given nameservers.
// Returns an *APIError with status 409 (see IsConflict) when the zone exists.
func (c *Client) CreateZone(ctx context.Context, apex string, ns []string) error {
	nameservers := make([]string, 0, len(ns))
	for _, n := range ns {
		nameservers = append(nameservers, ensureDot(n))
	}
	reqBody := createZoneRequest{
		Name:        zoneID(apex),
		Kind:        "Native",
		Nameservers: nameservers,
	}
	_, err := c.do(ctx, http.MethodPost, "/api/v1/servers/localhost/zones", reqBody)
	return err
}

// DeleteZone removes a zone entirely.
func (c *Client) DeleteZone(ctx context.Context, apex string) error {
	_, err := c.do(ctx, http.MethodDelete, "/api/v1/servers/localhost/zones/"+zoneID(apex), nil)
	return err
}

// GetZone returns the zone with its current rrsets.
func (c *Client) GetZone(ctx context.Context, apex string) (*Zone, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/servers/localhost/zones/"+zoneID(apex), nil)
	if err != nil {
		return nil, err
	}
	var z Zone
	if err := json.Unmarshal(raw, &z); err != nil {
		return nil, err
	}
	return &z, nil
}

// ListRecords returns the zone's rrsets.
func (c *Client) ListRecords(ctx context.Context, apex string) ([]RRSet, error) {
	z, err := c.GetZone(ctx, apex)
	if err != nil {
		return nil, err
	}
	return z.RRSets, nil
}

type patchRequest struct {
	RRSets []RRSet `json:"rrsets"`
}

// UpsertRecord replaces the rrset (name, rrType) in the zone with the given
// contents and TTL. name may be relative or absolute; it is qualified against
// the apex and normalized to a FQDN.
func (c *Client) UpsertRecord(ctx context.Context, apex, name, rrType string, ttl int, contents []string) error {
	records := make([]Record, 0, len(contents))
	for _, ct := range contents {
		records = append(records, Record{Content: ct, Disabled: false})
	}
	rr := RRSet{
		Name:       QualifyName(apex, name),
		Type:       strings.ToUpper(rrType),
		TTL:        ttl,
		ChangeType: "REPLACE",
		Records:    records,
	}
	return c.patch(ctx, apex, rr)
}

// DeleteRecord removes the rrset (name, rrType) from the zone.
func (c *Client) DeleteRecord(ctx context.Context, apex, name, rrType string) error {
	rr := RRSet{
		Name:       QualifyName(apex, name),
		Type:       strings.ToUpper(rrType),
		ChangeType: "DELETE",
	}
	return c.patch(ctx, apex, rr)
}

func (c *Client) patch(ctx context.Context, apex string, rr RRSet) error {
	_, err := c.do(ctx, http.MethodPatch, "/api/v1/servers/localhost/zones/"+zoneID(apex),
		patchRequest{RRSets: []RRSet{rr}})
	return err
}

// QualifyName resolves a (possibly relative) record name against the apex and
// returns the FQDN-with-trailing-dot form. An empty name or "@" maps to the apex.
func QualifyName(apex, name string) string {
	apexDot := ensureDot(apex)
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" || name == "@" {
		return apexDot
	}
	nameDot := ensureDot(name)
	if nameDot == apexDot || strings.HasSuffix(nameDot, "."+apexDot) {
		return nameDot
	}
	return strings.TrimSuffix(name, ".") + "." + apexDot
}
