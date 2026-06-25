package telemetry

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dada-tuda/console/backend/internal/prometheus"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// We decode into the OTLP data-layer messages MetricsData / LogsData rather than
// the collector ExportMetricsServiceRequest / ExportLogsServiceRequest. The
// data messages are wire- and JSON-compatible (both carry the single field 1
// `resource_metrics` / `resource_logs`) and, unlike the collector service
// packages, pull no gRPC / grpc-gateway dependency tree. This keeps the gateway
// lean while still using the official, generated OTLP types (ADR-012: do not
// hand-roll OTLP).

// Tenant carries the authoritative tenancy labels resolved from the dmon_ key's
// monitoring_apps row. The OTLP payload is NEVER trusted for these (ADR-012 §4):
// a client may set any service.name / resource attributes, but org/project/
// environment/app come only from here.
type Tenant struct {
	OrgID         string
	ProjectID     string
	Environment   string
	MonitoringApp string
	// MaxLabels caps the number of client attribute-derived labels merged onto a
	// single series (cardinality guard). Authoritative labels do not count.
	MaxLabels int
}

// reservedLabels are the authoritative label names the gateway injects. Client
// attributes may never overwrite them (tenant-spoofing guard).
var reservedLabels = map[string]struct{}{
	"__name__": {}, "org_id": {}, "project_id": {}, "environment": {},
	"monitoring_app": {}, "source": {}, "le": {},
}

const serviceNameKey = "service.name"

// isJSONContentType reports whether the OTLP/HTTP request carries JSON (vs the
// SDK-default application/x-protobuf).
func isJSONContentType(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "application/json")
}

// DecodeMetrics unmarshals an OTLP/HTTP metrics export request. Content-Type
// selects the codec: application/json -> protojson, anything else -> protobuf
// (proto.Unmarshal), matching the OTLP/HTTP spec where x-protobuf is the default.
func DecodeMetrics(contentType string, body []byte) (*metricsv1.MetricsData, error) {
	req := &metricsv1.MetricsData{}
	if isJSONContentType(contentType) {
		if err := protojson.Unmarshal(body, req); err != nil {
			return nil, fmt.Errorf("otlp metrics json: %w", err)
		}
		return req, nil
	}
	if err := proto.Unmarshal(body, req); err != nil {
		return nil, fmt.Errorf("otlp metrics protobuf: %w", err)
	}
	return req, nil
}

// DecodeLogs unmarshals an OTLP/HTTP logs export request (codec per Content-Type).
func DecodeLogs(contentType string, body []byte) (*logsv1.LogsData, error) {
	req := &logsv1.LogsData{}
	if isJSONContentType(contentType) {
		if err := protojson.Unmarshal(body, req); err != nil {
			return nil, fmt.Errorf("otlp logs json: %w", err)
		}
		return req, nil
	}
	if err := proto.Unmarshal(body, req); err != nil {
		return nil, fmt.Errorf("otlp logs protobuf: %w", err)
	}
	return req, nil
}

// anyValueString flattens an OTLP AnyValue to a label/field string. Nested
// kvlist/array values are rendered compactly; this is best-effort for labels.
func anyValueString(v *commonpb.AnyValue) string {
	if v == nil {
		return ""
	}
	switch v.Value.(type) {
	case *commonpb.AnyValue_StringValue:
		return v.GetStringValue()
	case *commonpb.AnyValue_BoolValue:
		return strconv.FormatBool(v.GetBoolValue())
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(v.GetIntValue(), 10)
	case *commonpb.AnyValue_DoubleValue:
		return strconv.FormatFloat(v.GetDoubleValue(), 'g', -1, 64)
	case *commonpb.AnyValue_BytesValue:
		return string(v.GetBytesValue())
	default:
		// array / kvlist / empty — fall back to the proto string form.
		return strings.TrimSpace(v.String())
	}
}

// attrLookup returns the string value of an attribute by key, or "".
func attrLookup(attrs []*commonpb.KeyValue, key string) string {
	for _, kv := range attrs {
		if kv.GetKey() == key {
			return anyValueString(kv.GetValue())
		}
	}
	return ""
}

// mergeAttrLabels copies client attributes into dst as labels: keys sanitized to
// valid Prometheus label names, reserved/authoritative names skipped, capped at
// max. Deterministic (input order). Returns the count added.
func mergeAttrLabels(dst map[string]string, attrs []*commonpb.KeyValue, max int, added int) int {
	for _, kv := range attrs {
		if added >= max {
			break
		}
		name := SanitizeMetricName(kv.GetKey())
		if _, reserved := reservedLabels[name]; reserved {
			continue
		}
		if name == serviceNameKey || kv.GetKey() == serviceNameKey {
			continue // service.name maps to `source`, handled separately
		}
		if _, exists := dst[name]; exists {
			continue
		}
		dst[name] = anyValueString(kv.GetValue())
		added++
	}
	return added
}

// MetricsToSeries flattens an OTLP metrics export into Prometheus remote-write
// series, injecting authoritative tenant labels. Gauge and Sum number points
// become single samples; explicit-bucket Histograms expand to
// _bucket{le}/_sum/_count. Exponential histograms and summaries are dropped
// (v1) and counted in `dropped`. service.name (resource or datapoint attr) maps
// to the `source` label only.
func MetricsToSeries(req *metricsv1.MetricsData, t Tenant) (series []prometheus.WriteSeries, dropped int) {
	maxLabels := t.MaxLabels
	if maxLabels <= 0 {
		maxLabels = 30
	}
	for _, rm := range req.GetResourceMetrics() {
		resAttrs := rm.GetResource().GetAttributes()
		resSource := attrLookup(resAttrs, serviceNameKey)
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				name := SanitizeMetricName(m.GetName())
				switch {
				case m.GetGauge() != nil:
					for _, dp := range m.GetGauge().GetDataPoints() {
						series = append(series, t.numberSeries(name, dp, resAttrs, resSource, maxLabels))
					}
				case m.GetSum() != nil:
					for _, dp := range m.GetSum().GetDataPoints() {
						series = append(series, t.numberSeries(name, dp, resAttrs, resSource, maxLabels))
					}
				case m.GetHistogram() != nil:
					for _, dp := range m.GetHistogram().GetDataPoints() {
						series = append(series, t.histogramSeries(name, dp, resAttrs, resSource, maxLabels)...)
					}
				default:
					// ExponentialHistogram / Summary / unset — deferred (v1).
					dropped++
				}
			}
		}
	}
	return series, dropped
}
