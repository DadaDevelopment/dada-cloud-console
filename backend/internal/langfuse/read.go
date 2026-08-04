package langfuse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	tracesPath       = "/api/public/traces"
	observationsPath = "/api/public/observations"
)

const readTimeout = 4 * time.Second

// MaxPageLimit is the largest page Langfuse is asked for. The API accepts more,
// but its own documentation warns that large pages fail on wide payloads, and a
// chat turn is a wide payload.
const MaxPageLimit = 100

var defaultReadClient = &http.Client{Timeout: readTimeout}

// Meta is the pagination envelope every list endpoint returns. TotalItems is
// the interesting one: it answers "how many" without walking the pages.
type Meta struct {
	Page       int `json:"page"`
	Limit      int `json:"limit"`
	TotalItems int `json:"totalItems"`
	TotalPages int `json:"totalPages"`
}

// Trace is one recorded turn. Input and Output stay raw because what a producer
// puts in them is its own business -- this client must not force a shape on
// traces written by anything other than the console agent.
type Trace struct {
	ID        string          `json:"id"`
	Timestamp string          `json:"timestamp"`
	Name      string          `json:"name"`
	UserID    string          `json:"userId"`
	SessionID string          `json:"sessionId"`
	Input     json.RawMessage `json:"input"`
	Output    json.RawMessage `json:"output"`
	Metadata  json.RawMessage `json:"metadata"`
	Tags      []string        `json:"tags"`
}

// TraceList is the response of GET /api/public/traces.
type TraceList struct {
	Data []Trace `json:"data"`
	Meta Meta    `json:"meta"`
}

// Observation is one span, generation or tool call inside a trace.
type Observation struct {
	ID        string          `json:"id"`
	TraceID   string          `json:"traceId"`
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	StartTime string          `json:"startTime"`
	EndTime   string          `json:"endTime"`
	Input     json.RawMessage `json:"input"`
	Output    json.RawMessage `json:"output"`
	Level     string          `json:"level"`
}

// ObservationList is the response of GET /api/public/observations.
type ObservationList struct {
	Data []Observation `json:"data"`
	Meta Meta          `json:"meta"`
}

// TraceQuery selects traces. A zero value asks for everything, which is almost
// never what a caller wants -- UserID or SessionID is expected.
type TraceQuery struct {
	UserID        string
	SessionID     string
	Name          string
	FromTimestamp time.Time
	ToTimestamp   time.Time
	OrderBy       string
	Page          int
	Limit         int
	Fields        string
}

func (q TraceQuery) values() url.Values {
	v := url.Values{}
	if q.UserID != "" {
		v.Set("userId", q.UserID)
	}
	if q.SessionID != "" {
		v.Set("sessionId", q.SessionID)
	}
	if q.Name != "" {
		v.Set("name", q.Name)
	}
	if !q.FromTimestamp.IsZero() {
		v.Set("fromTimestamp", q.FromTimestamp.UTC().Format(time.RFC3339))
	}
	if !q.ToTimestamp.IsZero() {
		v.Set("toTimestamp", q.ToTimestamp.UTC().Format(time.RFC3339))
	}
	if q.OrderBy != "" {
		v.Set("orderBy", q.OrderBy)
	}
	if q.Page > 0 {
		v.Set("page", strconv.Itoa(q.Page))
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Fields != "" {
		v.Set("fields", q.Fields)
	}
	return v
}

// ObservationQuery selects observations, normally all of one trace.
type ObservationQuery struct {
	TraceID string
	UserID  string
	Type    string
	Name    string
	Page    int
	Limit   int
}

func (q ObservationQuery) values() url.Values {
	v := url.Values{}
	if q.TraceID != "" {
		v.Set("traceId", q.TraceID)
	}
	if q.UserID != "" {
		v.Set("userId", q.UserID)
	}
	if q.Type != "" {
		v.Set("type", q.Type)
	}
	if q.Name != "" {
		v.Set("name", q.Name)
	}
	if q.Page > 0 {
		v.Set("page", strconv.Itoa(q.Page))
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	return v
}

// ListTraces reads one page of traces.
func (c *Client) ListTraces(ctx context.Context, q TraceQuery) (*TraceList, error) {
	var out TraceList
	if err := c.getJSON(ctx, tracesPath, q.values(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CountTraces answers how many traces match without transferring any of them.
// It asks for the smallest possible page and reads meta.totalItems, which is
// what makes a daily message cap affordable against a remote store.
func (c *Client) CountTraces(ctx context.Context, q TraceQuery) (int, error) {
	q.Page = 1
	q.Limit = 1
	q.Fields = "core"
	list, err := c.ListTraces(ctx, q)
	if err != nil {
		return 0, err
	}
	return list.Meta.TotalItems, nil
}

// ListObservations reads one page of observations.
func (c *Client) ListObservations(ctx context.Context, q ObservationQuery) (*ObservationList, error) {
	var out ObservationList
	if err := c.getJSON(ctx, observationsPath, q.values(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	if !c.Configured() {
		return fmt.Errorf("langfuse read: client is not configured")
	}

	client := c.HTTPClient
	if client == nil {
		client = defaultReadClient
	}

	endpoint := c.Host + path
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("langfuse read: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(c.PublicKey, c.SecretKey)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("langfuse read %s: %w", path, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxReadBody))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("langfuse read %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("langfuse read %s: decode: %w", path, err)
	}
	return nil
}

const maxReadBody = 8 << 20
