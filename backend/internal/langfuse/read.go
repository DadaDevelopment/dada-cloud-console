package langfuse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const observationsPath = "/api/public/v2/observations"

const readTimeout = 4 * time.Second

// MaxPageLimit is the largest page Langfuse is asked for. The v2 observations
// API caps a page at 1000 rows; a transcript message is a wide row, so pages
// stay well under that.
const MaxPageLimit = 250

// defaultLookback bounds a query that names no window. The v2 observations
// API refuses an unbounded time range, and a transcript older than this is
// past what the memory summary keeps anyway.
const defaultLookback = 30 * 24 * time.Hour

// maxCountPages bounds CountTraces: the daily cap it serves is in the tens,
// so walking further means the count is already far past any limit.
const maxCountPages = 10

// readFields is the field groups every read asks for: identity, io and
// metadata are what the transcript store decodes, time gives ordering.
const readFields = "core,basic,io,metadata,time,trace_context"

var defaultReadClient = &http.Client{Timeout: readTimeout}

// Meta is the pagination envelope of the v2 list endpoints: cursor pagination
// only, a non-empty Cursor means another page exists.
type Meta struct {
	Cursor string `json:"cursor"`
}

// Trace is one recorded turn as seen through its root observation, which is
// how Langfuse v4 represents a trace. Input and Output stay raw because what a
// producer puts in them is its own business -- this client must not force a
// shape on traces written by anything other than the console agent.
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

// TraceList is one page of traces, newest first.
type TraceList struct {
	Data []Trace `json:"data"`
	Meta Meta    `json:"meta"`
}

// Observation is one span, generation or tool call inside a trace.
type Observation struct {
	ID                  string          `json:"id"`
	TraceID             string          `json:"traceId"`
	ParentObservationID string          `json:"parentObservationId"`
	Type                string          `json:"type"`
	Name                string          `json:"name"`
	TraceName           string          `json:"traceName"`
	StartTime           string          `json:"startTime"`
	EndTime             string          `json:"endTime"`
	UserID              string          `json:"userId"`
	SessionID           string          `json:"sessionId"`
	Input               json.RawMessage `json:"input"`
	Output              json.RawMessage `json:"output"`
	Metadata            json.RawMessage `json:"metadata"`
	Tags                []string        `json:"tags"`
	Level               string          `json:"level"`
	IsRootObservation   bool            `json:"isRootObservation"`
}

// ObservationList is the response of GET /api/public/v2/observations.
type ObservationList struct {
	Data []Observation `json:"data"`
	Meta Meta          `json:"meta"`
}

// TraceQuery selects traces. A zero value asks for everything in the default
// window, which is almost never what a caller wants -- UserID or SessionID is
// expected. Name matches the trace name.
type TraceQuery struct {
	UserID        string
	SessionID     string
	Name          string
	FromTimestamp time.Time
	ToTimestamp   time.Time
	Cursor        string
	Limit         int
}

func (q TraceQuery) values() url.Values {
	v := url.Values{}
	v.Set("isRootObservation", "true")
	v.Set("fields", readFields)
	if q.UserID != "" {
		v.Set("userId", q.UserID)
	}
	if q.SessionID != "" {
		v.Set("sessionId", q.SessionID)
	}
	if q.Name != "" {
		v.Set("name", q.Name)
	}
	setWindow(v, q.FromTimestamp, q.ToTimestamp)
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	return v
}

func setWindow(v url.Values, from, to time.Time) {
	now := time.Now()
	if from.IsZero() {
		from = now.Add(-defaultLookback)
	}
	if to.IsZero() {
		to = now.Add(time.Minute)
	}
	v.Set("fromStartTime", from.UTC().Format(time.RFC3339))
	v.Set("toStartTime", to.UTC().Format(time.RFC3339))
}

// ObservationQuery selects observations, normally all of one trace.
type ObservationQuery struct {
	TraceID       string
	UserID        string
	Type          string
	Name          string
	FromStartTime time.Time
	ToStartTime   time.Time
	Cursor        string
	Limit         int
}

func (q ObservationQuery) values() url.Values {
	v := url.Values{}
	v.Set("fields", readFields)
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
	setWindow(v, q.FromStartTime, q.ToStartTime)
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	return v
}

// ListTraces reads one page of traces (root observations), newest first.
func (c *Client) ListTraces(ctx context.Context, q TraceQuery) (*TraceList, error) {
	var page ObservationList
	if err := c.getJSON(ctx, observationsPath, q.values(), &page); err != nil {
		return nil, err
	}
	out := &TraceList{Meta: page.Meta, Data: make([]Trace, 0, len(page.Data))}
	for _, o := range page.Data {
		out.Data = append(out.Data, o.trace())
	}
	sort.SliceStable(out.Data, func(i, j int) bool { return out.Data[i].Timestamp > out.Data[j].Timestamp })
	return out, nil
}

func (o Observation) trace() Trace {
	name := o.TraceName
	if name == "" {
		name = o.Name
	}
	return Trace{
		ID:        o.TraceID,
		Timestamp: o.StartTime,
		Name:      name,
		UserID:    o.UserID,
		SessionID: o.SessionID,
		Input:     o.Input,
		Output:    o.Output,
		Metadata:  o.Metadata,
		Tags:      o.Tags,
	}
}

// CountTraces answers how many traces match. The v2 API has no total, so the
// pages are walked with the widest page and only ids transferred; the walk
// stops at maxCountPages, which for the daily cap it serves is already far
// past any limit.
func (c *Client) CountTraces(ctx context.Context, q TraceQuery) (int, error) {
	v := q.values()
	v.Set("fields", "core")
	v.Set("limit", "1000")
	total := 0
	for page := 0; page < maxCountPages; page++ {
		var out ObservationList
		if err := c.getJSON(ctx, observationsPath, v, &out); err != nil {
			return 0, err
		}
		total += len(out.Data)
		if out.Meta.Cursor == "" {
			break
		}
		v.Set("cursor", out.Meta.Cursor)
	}
	return total, nil
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
	if resp.StatusCode == http.StatusTooManyRequests {
		return &RateLimitedError{Path: path, Body: strings.TrimSpace(string(raw)), ResetAt: rateLimitReset(raw, resp.Header.Get("Retry-After"), time.Now())}
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("langfuse read %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("langfuse read %s: decode: %w", path, err)
	}
	return nil
}

const maxReadBody = 8 << 20

// RateLimitedError is a 429 from the public API. Langfuse cloud counts some
// endpoints per day (v2/metrics allows 100 requests in 24 hours), so a caller
// that retries on its own clock only spends the next window early; ResetAt
// is when the window reopens, zero when the answer did not say.
type RateLimitedError struct {
	Path    string
	Body    string
	ResetAt time.Time
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("langfuse read %s: status 429: %s", e.Path, e.Body)
}

// rateLimitReset reads the reopening time from the body's details.resetAt,
// then details.retryAfterSeconds, then the Retry-After header.
func rateLimitReset(body []byte, retryAfter string, now time.Time) time.Time {
	var parsed struct {
		Details struct {
			ResetAt           string  `json:"resetAt"`
			RetryAfterSeconds float64 `json:"retryAfterSeconds"`
		} `json:"details"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		if t, err := time.Parse(time.RFC3339Nano, parsed.Details.ResetAt); err == nil {
			return t
		}
		if parsed.Details.RetryAfterSeconds > 0 {
			return now.Add(time.Duration(parsed.Details.RetryAfterSeconds * float64(time.Second)))
		}
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && secs > 0 {
		return now.Add(time.Duration(secs) * time.Second)
	}
	return time.Time{}
}
