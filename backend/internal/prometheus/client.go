// Package prometheus is a lean, read-only client for the Prometheus HTTP query
// API, used by the console backend to read back the VM/container metrics that
// the portainer-agent's node_exporter/cAdvisor sidecars remote_write into a
// central Prometheus.
//
// It mirrors the shape of internal/portainer (a minimal read-only proxy whose
// New returns nil when unconfigured so callers can treat the feature as
// disabled). Only the two query endpoints the UI needs are implemented.
package prometheus

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

// Client is a read-only Prometheus query client.
type Client struct {
	baseURL    string
	user, pass string
	httpClient *http.Client
}

// New creates a read-only Prometheus client. Returns nil when baseURL is empty
// so callers can treat the metrics feature as disabled. baseURL is the API root
// (e.g. https://prometheus.example.com), not a /api/v1/... path.
func New(baseURL, user, pass string) *Client {
	if baseURL == "" {
		return nil
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		user:       user,
		pass:       pass,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// Point is a single (timestamp, value) sample. Prometheus encodes each as a
// two-element array [<unix-seconds float>, "<value string>"]; Point.UnmarshalJSON
// decodes that tuple. NaN/Inf values are skipped by the higher-level decoder.
type Point struct {
	T float64 `json:"t"` // unix seconds
	V float64 `json:"v"`
}

// UnmarshalJSON parses Prometheus' [unixSeconds, "value-string"] sample tuple.
func (p *Point) UnmarshalJSON(b []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if len(raw) != 2 {
		return fmt.Errorf("prometheus sample: expected 2 elements, got %d", len(raw))
	}
	if err := json.Unmarshal(raw[0], &p.T); err != nil {
		return fmt.Errorf("prometheus sample timestamp: %w", err)
	}
	var vs string
	if err := json.Unmarshal(raw[1], &vs); err != nil {
		return fmt.Errorf("prometheus sample value: %w", err)
	}
	f, err := strconv.ParseFloat(vs, 64)
	if err != nil {
		return fmt.Errorf("prometheus sample value %q: %w", vs, err)
	}
	p.V = f
	return nil
}

// Series is one matrix result: a label set plus its time-ordered points.
type Series struct {
	Metric map[string]string `json:"metric"`
	Points []Point           `json:"values"`
}

// Sample is one instant-vector result.
type Sample struct {
	Metric map[string]string
	Point  Point
}

// promEnvelope is the standard Prometheus query response wrapper.
type promEnvelope struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	} `json:"data"`
}

func (c *Client) get(ctx context.Context, path string, q url.Values) (*promEnvelope, error) {
	u := c.baseURL + path + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // cap 4 MiB
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("prometheus GET %s: status %d: %s", path, resp.StatusCode, string(body))
	}
	var env promEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("prometheus decode %s: %w", path, err)
	}
	if env.Status != "success" {
		return nil, fmt.Errorf("prometheus query failed: %s", env.Error)
	}
	return &env, nil
}

// QueryRange runs /api/v1/query_range and returns matrix series.
func (c *Client) QueryRange(ctx context.Context, promQL string, start, end time.Time, step time.Duration) ([]Series, error) {
	q := url.Values{}
	q.Set("query", promQL)
	q.Set("start", strconv.FormatInt(start.Unix(), 10))
	q.Set("end", strconv.FormatInt(end.Unix(), 10))
	q.Set("step", strconv.Itoa(int(step.Seconds())))
	env, err := c.get(ctx, "/api/v1/query_range", q)
	if err != nil {
		return nil, err
	}
	var series []Series
	if err := json.Unmarshal(env.Data.Result, &series); err != nil {
		return nil, fmt.Errorf("prometheus matrix decode: %w", err)
	}
	return series, nil
}

// QueryInstant runs /api/v1/query and returns instant-vector samples.
func (c *Client) QueryInstant(ctx context.Context, promQL string, ts time.Time) ([]Sample, error) {
	q := url.Values{}
	q.Set("query", promQL)
	if !ts.IsZero() {
		q.Set("time", strconv.FormatInt(ts.Unix(), 10))
	}
	env, err := c.get(ctx, "/api/v1/query", q)
	if err != nil {
		return nil, err
	}
	// vector result: [{ "metric": {...}, "value": [ts, "v"] }]
	var vec []struct {
		Metric map[string]string `json:"metric"`
		Value  Point             `json:"value"`
	}
	if err := json.Unmarshal(env.Data.Result, &vec); err != nil {
		return nil, fmt.Errorf("prometheus vector decode: %w", err)
	}
	out := make([]Sample, 0, len(vec))
	for _, v := range vec {
		out = append(out, Sample{Metric: v.Metric, Point: v.Value})
	}
	return out, nil
}

// EscapeLabelValue escapes a string for safe interpolation into a PromQL label
// matcher value (double-quoted). Prevents query injection via VM/app names.
func EscapeLabelValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}
