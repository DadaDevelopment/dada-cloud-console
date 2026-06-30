package prometheus

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/golang/snappy"
	"google.golang.org/protobuf/encoding/protowire"
)

// WriteClient pushes samples to a Prometheus remote-write receiver (Prometheus
// started with --web.enable-remote-write-receiver). It hand-encodes the
// remote-write WriteRequest protobuf and snappy-compresses it — this avoids
// pulling the whole prometheus/prometheus dependency tree for four tiny
// messages. New returns nil when unconfigured so callers can treat ingestion as
// disabled (mirrors the read client and internal/portainer).
type WriteClient struct {
	endpoint   string
	user, pass string
	httpClient *http.Client
}

// NewWriteClient builds a remote-write client. baseURL is the receiver API root
// (e.g. https://prometheus.example.com); /api/v1/write is appended when no
// explicit remote-write path is present. A URL that already ends in the
// Prometheus path (/api/v1/write) or the Grafana Mimir path (/api/v1/push) is
// used as-is. Returns nil when baseURL is empty.
func NewWriteClient(baseURL, user, pass string) *WriteClient {
	if baseURL == "" {
		return nil
	}
	endpoint := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(endpoint, "/api/v1/write") && !strings.HasSuffix(endpoint, "/api/v1/push") {
		endpoint += "/api/v1/write"
	}
	return &WriteClient{
		endpoint:   endpoint,
		user:       user,
		pass:       pass,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// WriteSeries is one labeled sample to push. Labels must include __name__.
type WriteSeries struct {
	Labels      map[string]string
	Value       float64
	TimestampMS int64
}

// Write encodes the series as a remote-write WriteRequest (protobuf + snappy)
// and POSTs it. orgID is the tenant: it is stamped as X-Scope-OrgID (or
// DefaultTenant when empty) so a multi-tenant Grafana Mimir receiver scopes the
// samples to one tenant. A plain Prometheus receiver ignores the header, so
// setting it unconditionally is backward-compatible. A nil receiver is a no-op
// error so callers nil-check first.
func (c *WriteClient) Write(ctx context.Context, orgID string, series []WriteSeries) error {
	if c == nil {
		return fmt.Errorf("remote-write not configured")
	}
	if len(series) == 0 {
		return nil
	}
	payload := marshalWriteRequest(series)
	compressed := snappy.Encode(nil, payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(compressed))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	tenant := orgID
	if tenant == "" {
		tenant = DefaultTenant
	}
	req.Header.Set("X-Scope-OrgID", tenant)
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("prometheus remote-write: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("prometheus remote-write: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// --- minimal protobuf wire encoding for the remote-write WriteRequest ---
//
//   message WriteRequest { repeated TimeSeries timeseries = 1; }
//   message TimeSeries   { repeated Label labels = 1; repeated Sample samples = 2; }
//   message Label        { string name = 1; string value = 2; }
//   message Sample       { double value = 1; int64 timestamp = 2; }

func marshalWriteRequest(series []WriteSeries) []byte {
	var out []byte
	for _, ts := range series {
		msg := marshalTimeSeries(ts)
		out = protowire.AppendTag(out, 1, protowire.BytesType)
		out = protowire.AppendBytes(out, msg)
	}
	return out
}

func marshalTimeSeries(ts WriteSeries) []byte {
	var out []byte
	// labels (field 1), sorted by name for deterministic output
	names := make([]string, 0, len(ts.Labels))
	for k := range ts.Labels {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		out = protowire.AppendTag(out, 1, protowire.BytesType)
		out = protowire.AppendBytes(out, marshalLabel(n, ts.Labels[n]))
	}
	// sample (field 2)
	out = protowire.AppendTag(out, 2, protowire.BytesType)
	out = protowire.AppendBytes(out, marshalSample(ts.Value, ts.TimestampMS))
	return out
}

func marshalLabel(name, value string) []byte {
	var out []byte
	out = protowire.AppendTag(out, 1, protowire.BytesType)
	out = protowire.AppendString(out, name)
	out = protowire.AppendTag(out, 2, protowire.BytesType)
	out = protowire.AppendString(out, value)
	return out
}

func marshalSample(v float64, tsMS int64) []byte {
	var out []byte
	out = protowire.AppendTag(out, 1, protowire.Fixed64Type)
	out = protowire.AppendFixed64(out, math.Float64bits(v))
	out = protowire.AppendTag(out, 2, protowire.VarintType)
	out = protowire.AppendVarint(out, uint64(tsMS))
	return out
}
