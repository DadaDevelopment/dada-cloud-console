package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dada-tuda/console/backend/internal/logsearch"
	"github.com/dada-tuda/console/backend/internal/prometheus"
	"github.com/dada-tuda/console/backend/internal/telemetry"
	"github.com/golang/snappy"
	"github.com/google/uuid"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// ---- fakes ----

type fakeKeyStore struct{ rows map[string][]keyRow }

func (f fakeKeyStore) candidatesByPrefix(_ context.Context, prefix string) ([]keyRow, error) {
	return f.rows[prefix], nil
}

// mintKey returns a usable dmon_ key and the keyRow the store should return for
// it (tenant owner/project/env/app + scopes).
func mintKey(t *testing.T, owner, project uuid.UUID, env, app string, scopes []string) (string, keyRow) {
	t.Helper()
	full, _, hash, err := telemetry.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	o := owner
	return full, keyRow{
		appID:     uuid.New(),
		hash:      hash,
		scopes:    scopes,
		owner:     &o,
		projectID: project,
		envName:   env,
		appName:   app,
	}
}

// capturing prometheus remote-write receiver. Decodes the snappy+protobuf body
// into label sets so tests can assert authoritative tenancy.
type capturedSeries struct {
	mu     sync.Mutex
	series []map[string]string
}

func (c *capturedSeries) all() []map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]map[string]string(nil), c.series...)
}

func newPromStub(t *testing.T, cap *capturedSeries) *prometheus.WriteClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		raw, err := snappy.Decode(nil, body)
		if err != nil {
			t.Errorf("snappy decode: %v", err)
			w.WriteHeader(500)
			return
		}
		labelsets := decodeRemoteWrite(raw)
		cap.mu.Lock()
		cap.series = append(cap.series, labelsets...)
		cap.mu.Unlock()
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	return prometheus.NewWriteClient(srv.URL, "", "")
}

// decodeRemoteWrite parses a Prometheus remote-write WriteRequest into per-series
// label maps. Schema: WriteRequest{ts=1}, TimeSeries{labels=1}, Label{name=1,value=2}.
func decodeRemoteWrite(b []byte) []map[string]string {
	var out []map[string]string
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if num == 1 && typ == protowire.BytesType {
			ts, n := protowire.ConsumeBytes(b)
			b = b[n:]
			out = append(out, decodeTimeSeries(ts))
		} else {
			n := protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				break
			}
			b = b[n:]
		}
	}
	return out
}

func decodeTimeSeries(b []byte) map[string]string {
	labels := map[string]string{}
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if num == 1 && typ == protowire.BytesType {
			lb, n := protowire.ConsumeBytes(b)
			b = b[n:]
			name, val := decodeLabel(lb)
			labels[name] = val
		} else {
			n := protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				break
			}
			b = b[n:]
		}
	}
	return labels
}

func decodeLabel(b []byte) (name, value string) {
	for len(b) > 0 {
		num, _, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		s, n := protowire.ConsumeBytes(b)
		b = b[n:]
		if num == 1 {
			name = string(s)
		} else if num == 2 {
			value = string(s)
		}
	}
	return
}

// capturing ES writer stub.
type capturedLogs struct {
	mu   sync.Mutex
	docs []map[string]any
}

func newESStub(t *testing.T, cap *capturedLogs) *logsearch.WriteClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var doc map[string]any
		_ = json.NewDecoder(r.Body).Decode(&doc)
		cap.mu.Lock()
		cap.docs = append(cap.docs, doc)
		cap.mu.Unlock()
		w.WriteHeader(201)
	}))
	t.Cleanup(srv.Close)
	return logsearch.NewWriteClient(srv.URL, "", "dada-app-logs")
}

// ---- helpers ----

func otlpGauge(name string, val float64, attrs ...*commonpb.KeyValue) []byte {
	md := &metricsv1.MetricsData{ResourceMetrics: []*metricsv1.ResourceMetrics{{
		Resource: &resourcev1.Resource{Attributes: attrs},
		ScopeMetrics: []*metricsv1.ScopeMetrics{{Metrics: []*metricsv1.Metric{
			{Name: name, Data: &metricsv1.Metric_Gauge{Gauge: &metricsv1.Gauge{
				DataPoints: []*metricsv1.NumberDataPoint{{Value: &metricsv1.NumberDataPoint_AsDouble{AsDouble: val}}},
			}}},
		}}},
	}}}
	b, _ := proto.Marshal(md)
	return b
}

func postOTLP(t *testing.T, h http.Handler, path, key, ct string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func newTestServer(store keyStore, prom *prometheus.WriteClient, es *logsearch.WriteClient, cfg Config) http.Handler {
	return NewServer(store, prom, es, cfg).Handler()
}

// ---- tests ----

// Cross-tenant isolation: org A's key forwards series labeled with org A — even
// when the OTLP payload tries to spoof org_id / a foreign service identity. And
// org B's key lands as org B. Labels come solely from the verified key's row.
func TestCrossTenantIsolation(t *testing.T) {
	orgA, projA := uuid.New(), uuid.New()
	orgB, projB := uuid.New(), uuid.New()
	keyA, rowA := mintKey(t, orgA, projA, "prod", "fleet-a", []string{"metrics:write"})
	keyB, rowB := mintKey(t, orgB, projB, "prod", "fleet-b", []string{"metrics:write"})

	store := fakeKeyStore{rows: map[string][]keyRow{
		telemetry.KeyLookupPrefix(keyA): {rowA},
		telemetry.KeyLookupPrefix(keyB): {rowB},
	}}
	cap := &capturedSeries{}
	h := newTestServer(store, newPromStub(t, cap), nil, Config{})

	spoof := otlpGauge("cpu", 1, // payload lies about org_id
		&commonpb.KeyValue{Key: "org_id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: orgB.String()}}})

	if rr := postOTLP(t, h, "/v1/metrics", keyA, "application/x-protobuf", spoof); rr.Code != 200 {
		t.Fatalf("A status = %d, body=%s", rr.Code, rr.Body)
	}
	if rr := postOTLP(t, h, "/v1/metrics", keyB, "application/x-protobuf", otlpGauge("cpu", 2)); rr.Code != 200 {
		t.Fatalf("B status = %d", rr.Code)
	}

	got := cap.all()
	if len(got) != 2 {
		t.Fatalf("series = %d, want 2", len(got))
	}
	for _, s := range got {
		switch s["monitoring_app"] {
		case "fleet-a":
			if s["org_id"] != orgA.String() || s["project_id"] != projA.String() {
				t.Errorf("A leaked: org=%s proj=%s (want %s/%s)", s["org_id"], s["project_id"], orgA, projA)
			}
		case "fleet-b":
			if s["org_id"] != orgB.String() || s["project_id"] != projB.String() {
				t.Errorf("B wrong: %+v", s)
			}
		default:
			t.Errorf("unexpected app: %+v", s)
		}
	}
}

// A prefix collision (two rows share a prefix) must resolve to the row whose
// argon2id hash actually verifies — never the other tenant.
func TestPrefixCollisionResolvesByHash(t *testing.T) {
	orgA, orgB := uuid.New(), uuid.New()
	keyA, rowA := mintKey(t, orgA, uuid.New(), "prod", "a", []string{"metrics:write"})
	keyB, rowB := mintKey(t, orgB, uuid.New(), "prod", "b", []string{"metrics:write"})
	// Force both candidates under keyA's prefix bucket.
	store := fakeKeyStore{rows: map[string][]keyRow{
		telemetry.KeyLookupPrefix(keyA): {rowB, rowA}, // rowB first to prove hash, not order, decides
	}}
	res, err := resolveKey(context.Background(), store, keyA, 30)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.tenant.MonitoringApp != "a" || res.tenant.OrgID != orgA.String() {
		t.Errorf("resolved wrong tenant: %+v", res.tenant)
	}
	_ = keyB
}

// Missing/forged key -> 401.
func TestUnauthorized(t *testing.T) {
	store := fakeKeyStore{rows: map[string][]keyRow{}}
	h := newTestServer(store, nil, nil, Config{})
	if rr := postOTLP(t, h, "/v1/metrics", "", "application/x-protobuf", otlpGauge("x", 1)); rr.Code != 401 {
		t.Errorf("no key: status = %d, want 401", rr.Code)
	}
	if rr := postOTLP(t, h, "/v1/metrics", "dmon_forged_key_value", "application/x-protobuf", otlpGauge("x", 1)); rr.Code != 401 {
		t.Errorf("forged key: status = %d, want 401", rr.Code)
	}
}

// Scope gate: a logs-only key cannot write metrics (403), and vice-versa.
func TestScopeReject(t *testing.T) {
	key, row := mintKey(t, uuid.New(), uuid.New(), "prod", "x", []string{"logs:write"})
	store := fakeKeyStore{rows: map[string][]keyRow{telemetry.KeyLookupPrefix(key): {row}}}
	cap := &capturedSeries{}
	h := newTestServer(store, newPromStub(t, cap), nil, Config{})
	if rr := postOTLP(t, h, "/v1/metrics", key, "application/x-protobuf", otlpGauge("x", 1)); rr.Code != 403 {
		t.Errorf("status = %d, want 403", rr.Code)
	}
	if len(cap.all()) != 0 {
		t.Error("forwarded series despite scope reject")
	}
}

// Per-app rate limit -> 429 once the burst is drained.
func TestRateLimit(t *testing.T) {
	key, row := mintKey(t, uuid.New(), uuid.New(), "prod", "x", []string{"metrics:write"})
	store := fakeKeyStore{rows: map[string][]keyRow{telemetry.KeyLookupPrefix(key): {row}}}
	cap := &capturedSeries{}
	h := newTestServer(store, newPromStub(t, cap), nil, Config{RateLimitPerMin: 1})

	if rr := postOTLP(t, h, "/v1/metrics", key, "application/x-protobuf", otlpGauge("x", 1)); rr.Code != 200 {
		t.Fatalf("first status = %d", rr.Code)
	}
	if rr := postOTLP(t, h, "/v1/metrics", key, "application/x-protobuf", otlpGauge("x", 1)); rr.Code != 429 {
		t.Errorf("second status = %d, want 429", rr.Code)
	}
}

// Cardinality cap: more series than MaxSeriesPerReq -> 413, nothing forwarded.
func TestCardinalityCap(t *testing.T) {
	key, row := mintKey(t, uuid.New(), uuid.New(), "prod", "x", []string{"metrics:write"})
	store := fakeKeyStore{rows: map[string][]keyRow{telemetry.KeyLookupPrefix(key): {row}}}
	cap := &capturedSeries{}
	h := newTestServer(store, newPromStub(t, cap), nil, Config{MaxSeriesPerReq: 1})

	// two gauges -> two series -> over the cap of 1
	md := &metricsv1.MetricsData{ResourceMetrics: []*metricsv1.ResourceMetrics{{
		ScopeMetrics: []*metricsv1.ScopeMetrics{{Metrics: []*metricsv1.Metric{
			{Name: "a", Data: &metricsv1.Metric_Gauge{Gauge: &metricsv1.Gauge{DataPoints: []*metricsv1.NumberDataPoint{{Value: &metricsv1.NumberDataPoint_AsDouble{AsDouble: 1}}}}}},
			{Name: "b", Data: &metricsv1.Metric_Gauge{Gauge: &metricsv1.Gauge{DataPoints: []*metricsv1.NumberDataPoint{{Value: &metricsv1.NumberDataPoint_AsDouble{AsDouble: 2}}}}}},
		}}},
	}}}
	body, _ := proto.Marshal(md)
	if rr := postOTLP(t, h, "/v1/metrics", key, "application/x-protobuf", body); rr.Code != 413 {
		t.Errorf("status = %d, want 413", rr.Code)
	}
	if len(cap.all()) != 0 {
		t.Error("forwarded despite cardinality reject")
	}
}

// Logs path end-to-end through the ES stub with authoritative tenancy.
func TestOTLPLogsForward(t *testing.T) {
	org, proj := uuid.New(), uuid.New()
	key, row := mintKey(t, org, proj, "prod", "loggy", []string{"logs:write"})
	store := fakeKeyStore{rows: map[string][]keyRow{telemetry.KeyLookupPrefix(key): {row}}}
	cap := &capturedLogs{}
	h := newTestServer(store, nil, newESStub(t, cap), Config{})

	body := otlpLog("boom", "ERROR")
	if rr := postOTLP(t, h, "/v1/logs", key, "application/x-protobuf", body); rr.Code != 200 {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.docs) != 1 {
		t.Fatalf("docs = %d, want 1", len(cap.docs))
	}
	d := cap.docs[0]
	if d["message"] != "boom" || d["level"] != "ERROR" {
		t.Errorf("doc fields wrong: %+v", d)
	}
	if d["org_id"] != org.String() || d["project_id"] != proj.String() || d["monitoring_app"] != "loggy" {
		t.Errorf("tenancy wrong: %+v", d)
	}
}

func otlpLog(msg, sev string) []byte {
	ld := &logsv1.LogsData{ResourceLogs: []*logsv1.ResourceLogs{{
		ScopeLogs: []*logsv1.ScopeLogs{{LogRecords: []*logsv1.LogRecord{{
			SeverityText: sev,
			Body:         &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: msg}},
		}}}},
	}}}
	b, _ := proto.Marshal(ld)
	return b
}
