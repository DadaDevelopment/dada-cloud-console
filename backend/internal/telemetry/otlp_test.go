package telemetry

import (
	"testing"

	"github.com/dada-tuda/console/backend/internal/prometheus"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func strAttr(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

func testTenant() Tenant {
	return Tenant{OrgID: "org-1", ProjectID: "proj-1", Environment: "prod", MonitoringApp: "device-fleet", MaxLabels: 10}
}

// sample gauge + sum metrics export with a resource service.name and a spoof
// attempt at org_id via a resource attribute.
func sampleMetrics() *metricsv1.MetricsData {
	return &metricsv1.MetricsData{
		ResourceMetrics: []*metricsv1.ResourceMetrics{{
			Resource: &resourcev1.Resource{Attributes: []*commonpb.KeyValue{
				strAttr("service.name", "device-001"),
				strAttr("org_id", "ATTACKER-ORG"), // must be ignored (authoritative wins)
				strAttr("region", "eu"),
			}},
			ScopeMetrics: []*metricsv1.ScopeMetrics{{
				Metrics: []*metricsv1.Metric{
					{Name: "cpu.usage", Data: &metricsv1.Metric_Gauge{Gauge: &metricsv1.Gauge{
						DataPoints: []*metricsv1.NumberDataPoint{{
							TimeUnixNano: 1_700_000_000_000_000_000,
							Value:        &metricsv1.NumberDataPoint_AsDouble{AsDouble: 42.5},
						}},
					}}},
					{Name: "requests", Data: &metricsv1.Metric_Sum{Sum: &metricsv1.Sum{
						DataPoints: []*metricsv1.NumberDataPoint{{
							TimeUnixNano: 1_700_000_000_000_000_000,
							Value:        &metricsv1.NumberDataPoint_AsInt{AsInt: 7},
						}},
					}}},
				},
			}},
		}},
	}
}

// findSeries returns the first series whose __name__ matches and the labels.
func findSeries(t *testing.T, series []writeSeriesView, name string) writeSeriesView {
	t.Helper()
	for _, s := range series {
		if s.Labels["__name__"] == name {
			return s
		}
	}
	t.Fatalf("series %q not found", name)
	return writeSeriesView{}
}

type writeSeriesView struct {
	Labels map[string]string
	Value  float64
}

// Decode (protobuf) -> convert; assert values, int handling, and that the
// authoritative tenant labels override the spoofed payload attribute.
func TestMetricsProtobufRoundTrip(t *testing.T) {
	raw, err := proto.Marshal(sampleMetrics())
	if err != nil {
		t.Fatal(err)
	}
	data, err := DecodeMetrics("application/x-protobuf", raw)
	if err != nil {
		t.Fatalf("DecodeMetrics: %v", err)
	}
	series, dropped := MetricsToSeries(data, testTenant())
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	views := toViews(series)

	cpu := findSeries(t, views, "cpu_usage")
	if cpu.Value != 42.5 {
		t.Errorf("cpu value = %v, want 42.5", cpu.Value)
	}
	if cpu.Labels["org_id"] != "org-1" {
		t.Errorf("org_id = %q, want org-1 (authoritative, payload spoof must be ignored)", cpu.Labels["org_id"])
	}
	if cpu.Labels["project_id"] != "proj-1" || cpu.Labels["environment"] != "prod" || cpu.Labels["monitoring_app"] != "device-fleet" {
		t.Errorf("tenant labels wrong: %+v", cpu.Labels)
	}
	if cpu.Labels["source"] != "device-001" {
		t.Errorf("source = %q, want device-001 (service.name)", cpu.Labels["source"])
	}
	if cpu.Labels["region"] != "eu" {
		t.Errorf("region label = %q, want eu", cpu.Labels["region"])
	}
	req := findSeries(t, views, "requests")
	if req.Value != 7 {
		t.Errorf("requests (int sum) value = %v, want 7", req.Value)
	}
}

// protojson must produce the same conversion as protobuf (codec parity).
func TestMetricsJSONRoundTrip(t *testing.T) {
	raw, err := protojson.Marshal(sampleMetrics())
	if err != nil {
		t.Fatal(err)
	}
	data, err := DecodeMetrics("application/json", raw)
	if err != nil {
		t.Fatalf("DecodeMetrics json: %v", err)
	}
	series, _ := MetricsToSeries(data, testTenant())
	views := toViews(series)
	cpu := findSeries(t, views, "cpu_usage")
	if cpu.Value != 42.5 || cpu.Labels["org_id"] != "org-1" {
		t.Errorf("json path mismatch: %+v", cpu)
	}
}

// Explicit-bucket histogram expands to cumulative _bucket{le}, _sum, _count.
func TestHistogramExpansion(t *testing.T) {
	sum := 30.0
	md := &metricsv1.MetricsData{ResourceMetrics: []*metricsv1.ResourceMetrics{{
		ScopeMetrics: []*metricsv1.ScopeMetrics{{Metrics: []*metricsv1.Metric{
			{Name: "latency", Data: &metricsv1.Metric_Histogram{Histogram: &metricsv1.Histogram{
				DataPoints: []*metricsv1.HistogramDataPoint{{
					TimeUnixNano:   1_700_000_000_000_000_000,
					Count:          6,
					Sum:            &sum,
					ExplicitBounds: []float64{1, 5, 10},
					BucketCounts:   []uint64{1, 2, 2, 1}, // <=1:1, <=5:2, <=10:2, +Inf:1
				}},
			}}},
		}}},
	}}}
	series, dropped := MetricsToSeries(md, testTenant())
	if dropped != 0 {
		t.Fatalf("dropped = %d", dropped)
	}
	views := toViews(series)

	// cumulative le buckets
	want := map[string]float64{"1": 1, "5": 3, "10": 5, "+Inf": 6}
	got := map[string]float64{}
	var sawSum, sawCount bool
	for _, s := range views {
		switch s.Labels["__name__"] {
		case "latency_bucket":
			got[s.Labels["le"]] = s.Value
		case "latency_sum":
			sawSum = true
			if s.Value != 30 {
				t.Errorf("_sum = %v, want 30", s.Value)
			}
		case "latency_count":
			sawCount = true
			if s.Value != 6 {
				t.Errorf("_count = %v, want 6", s.Value)
			}
		}
	}
	for le, v := range want {
		if got[le] != v {
			t.Errorf("bucket le=%s = %v, want %v (got buckets: %+v)", le, got[le], v, got)
		}
	}
	if !sawSum || !sawCount {
		t.Errorf("missing _sum (%v) or _count (%v)", sawSum, sawCount)
	}
}

// Exponential histogram + summary are dropped (v1), counted in `dropped`.
func TestExponentialHistogramDropped(t *testing.T) {
	md := &metricsv1.MetricsData{ResourceMetrics: []*metricsv1.ResourceMetrics{{
		ScopeMetrics: []*metricsv1.ScopeMetrics{{Metrics: []*metricsv1.Metric{
			{Name: "exp", Data: &metricsv1.Metric_ExponentialHistogram{
				ExponentialHistogram: &metricsv1.ExponentialHistogram{
					DataPoints: []*metricsv1.ExponentialHistogramDataPoint{{TimeUnixNano: 1}},
				}}},
		}}},
	}}}
	series, dropped := MetricsToSeries(md, testTenant())
	if len(series) != 0 {
		t.Errorf("series = %d, want 0", len(series))
	}
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
}

// Logs: body->Message, severity_number->Level fallback, service.name->Source,
// authoritative tenancy from the Tenant, codec parity proto+json.
func TestLogsRoundTrip(t *testing.T) {
	build := func() *logsv1.LogsData {
		return &logsv1.LogsData{ResourceLogs: []*logsv1.ResourceLogs{{
			Resource: &resourcev1.Resource{Attributes: []*commonpb.KeyValue{strAttr("service.name", "sensor-7")}},
			ScopeLogs: []*logsv1.ScopeLogs{{LogRecords: []*logsv1.LogRecord{{
				TimeUnixNano:   1_700_000_000_000_000_000,
				SeverityNumber: logsv1.SeverityNumber_SEVERITY_NUMBER_ERROR,
				Body:           &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "wifi disconnected"}},
			}}}},
		}}}
	}
	for _, codec := range []struct {
		name string
		raw  func() []byte
		ct   string
	}{
		{"proto", func() []byte { b, _ := proto.Marshal(build()); return b }, "application/x-protobuf"},
		{"json", func() []byte { b, _ := protojson.Marshal(build()); return b }, "application/json"},
	} {
		t.Run(codec.name, func(t *testing.T) {
			data, err := DecodeLogs(codec.ct, codec.raw())
			if err != nil {
				t.Fatal(err)
			}
			docs := LogsToAppLogs(data, testTenant())
			if len(docs) != 1 {
				t.Fatalf("docs = %d, want 1", len(docs))
			}
			d := docs[0]
			if d.Message != "wifi disconnected" {
				t.Errorf("message = %q", d.Message)
			}
			if d.Level != "ERROR" {
				t.Errorf("level = %q, want ERROR", d.Level)
			}
			if d.Source != "sensor-7" {
				t.Errorf("source = %q, want sensor-7", d.Source)
			}
			if d.OrgID != "org-1" || d.ProjectID != "proj-1" || d.MonitoringApp != "device-fleet" {
				t.Errorf("tenancy wrong: %+v", d)
			}
		})
	}
}

// MaxLabels caps client attribute fan-out (cardinality guard); reserved labels
// are never overwritten.
func TestMaxLabelsCap(t *testing.T) {
	attrs := []*commonpb.KeyValue{}
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		attrs = append(attrs, strAttr(k, "v"))
	}
	md := &metricsv1.MetricsData{ResourceMetrics: []*metricsv1.ResourceMetrics{{
		Resource: &resourcev1.Resource{Attributes: attrs},
		ScopeMetrics: []*metricsv1.ScopeMetrics{{Metrics: []*metricsv1.Metric{
			{Name: "g", Data: &metricsv1.Metric_Gauge{Gauge: &metricsv1.Gauge{
				DataPoints: []*metricsv1.NumberDataPoint{{Value: &metricsv1.NumberDataPoint_AsDouble{AsDouble: 1}}},
			}}},
		}}},
	}}}
	tn := testTenant()
	tn.MaxLabels = 2
	series, _ := MetricsToSeries(md, tn)
	v := toViews(series)[0]
	attrCount := 0
	for k := range v.Labels {
		switch k {
		case "__name__", "org_id", "project_id", "environment", "monitoring_app", "source":
		default:
			attrCount++
		}
	}
	if attrCount > 2 {
		t.Errorf("attr labels = %d, want <= 2 (cap): %+v", attrCount, v.Labels)
	}
}

// toViews adapts prometheus.WriteSeries (unexported field access is fine here:
// the Labels/Value fields are exported).
func toViews(series []prometheus.WriteSeries) []writeSeriesView {
	out := make([]writeSeriesView, 0, len(series))
	for _, s := range series {
		out = append(out, writeSeriesView{Labels: s.Labels, Value: s.Value})
	}
	return out
}

// TestResolveSourcePrecedence locks the device-identity contract: source is
// resolved service.instance.id -> host.name -> service.name, with datapoint
// attrs overriding resource attrs per key.
func TestResolveSourcePrecedence(t *testing.T) {
	cases := []struct {
		name     string
		resAttrs []*commonpb.KeyValue
		dpAttrs  []*commonpb.KeyValue
		want     string
	}{
		{
			name:     "instance id wins over host and service",
			resAttrs: []*commonpb.KeyValue{strAttr("service.name", "svc"), strAttr("host.name", "h1"), strAttr("service.instance.id", "inst-1")},
			want:     "inst-1",
		},
		{
			name:     "host name wins over service name",
			resAttrs: []*commonpb.KeyValue{strAttr("service.name", "svc"), strAttr("host.name", "h1")},
			want:     "h1",
		},
		{
			name:     "service name is last resort",
			resAttrs: []*commonpb.KeyValue{strAttr("service.name", "svc")},
			want:     "svc",
		},
		{
			name:     "datapoint overrides resource for same key",
			resAttrs: []*commonpb.KeyValue{strAttr("service.instance.id", "res-inst")},
			dpAttrs:  []*commonpb.KeyValue{strAttr("service.instance.id", "dp-inst")},
			want:     "dp-inst",
		},
		{
			name:     "stronger resource key beats weaker datapoint key",
			resAttrs: []*commonpb.KeyValue{strAttr("service.instance.id", "res-inst")},
			dpAttrs:  []*commonpb.KeyValue{strAttr("service.name", "dp-svc")},
			want:     "res-inst",
		},
		{
			name: "none present yields empty",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveSource(tc.resAttrs, tc.dpAttrs); got != tc.want {
				t.Errorf("resolveSource = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestIdentityAttrsNotDuplicatedAsLabels ensures an identity attribute maps to
// `source` only and never also appears as a free series label.
func TestIdentityAttrsNotDuplicatedAsLabels(t *testing.T) {
	md := &metricsv1.MetricsData{ResourceMetrics: []*metricsv1.ResourceMetrics{{
		Resource: &resourcev1.Resource{Attributes: []*commonpb.KeyValue{
			strAttr("service.instance.id", "inst-9"),
			strAttr("host.name", "host-9"),
			strAttr("service.name", "svc-9"),
			strAttr("region", "eu"),
		}},
		ScopeMetrics: []*metricsv1.ScopeMetrics{{Metrics: []*metricsv1.Metric{{
			Name: "temp",
			Data: &metricsv1.Metric_Gauge{Gauge: &metricsv1.Gauge{DataPoints: []*metricsv1.NumberDataPoint{{
				Value: &metricsv1.NumberDataPoint_AsDouble{AsDouble: 1},
			}}}},
		}}}},
	}}}
	series, _ := MetricsToSeries(md, testTenant())
	if len(series) != 1 {
		t.Fatalf("series = %d, want 1", len(series))
	}
	got := series[0].Labels
	if got["source"] != "inst-9" {
		t.Errorf("source = %q, want inst-9", got["source"])
	}
	for _, k := range []string{"service.instance.id", "service_instance_id", "host.name", "host_name", "service.name", "service_name"} {
		if _, ok := got[k]; ok {
			t.Errorf("identity key %q leaked as a free label", k)
		}
	}
	if got["region"] != "eu" {
		t.Errorf("non-identity attr region = %q, want eu (should still be a label)", got["region"])
	}
}
