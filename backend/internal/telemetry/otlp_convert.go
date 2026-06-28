package telemetry

import (
	"strconv"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/logsearch"
	"github.com/dada-tuda/console/backend/internal/prometheus"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// authoritativeLabels returns the fixed tenant labels for a series. `source` is
// the device identity resolved per the contract (resolveSource): the only
// client-controlled value allowed, and only into `source`.
func (t Tenant) authoritativeLabels(name, source string) map[string]string {
	return map[string]string{
		"__name__":       name,
		"org_id":         t.OrgID,
		"project_id":     t.ProjectID,
		"environment":    t.Environment,
		"monitoring_app": t.MonitoringApp,
		"source":         source,
	}
}

// nanoToMS converts an OTLP time_unix_nano to epoch millis, defaulting to now
// when unset (0).
func nanoToMS(nano uint64) int64 {
	if nano == 0 {
		return time.Now().UnixMilli()
	}
	return int64(nano / 1_000_000)
}

// numberSeries converts a gauge/sum number data point to one sample.
func (t Tenant) numberSeries(name string, dp *metricsv1.NumberDataPoint, resAttrs []*commonpb.KeyValue, maxLabels int) prometheus.WriteSeries {
	source := resolveSource(resAttrs, dp.GetAttributes())
	labels := t.authoritativeLabels(name, source)
	added := mergeAttrLabels(labels, resAttrs, maxLabels, 0)
	mergeAttrLabels(labels, dp.GetAttributes(), maxLabels, added)

	val := dp.GetAsDouble()
	if _, isInt := dp.GetValue().(*metricsv1.NumberDataPoint_AsInt); isInt {
		val = float64(dp.GetAsInt())
	}
	return prometheus.WriteSeries{Labels: labels, Value: val, TimestampMS: nanoToMS(dp.GetTimeUnixNano())}
}

// histogramSeries expands one explicit-bucket histogram data point into the
// Prometheus convention: cumulative <name>_bucket{le}, plus <name>_sum and
// <name>_count. OTLP bucket_counts are per-bucket; Prometheus le-buckets are
// cumulative, so we accumulate. bucket_counts has len(explicit_bounds)+1 entries
// (the last is the +Inf overflow bucket).
func (t Tenant) histogramSeries(name string, dp *metricsv1.HistogramDataPoint, resAttrs []*commonpb.KeyValue, maxLabels int) []prometheus.WriteSeries {
	source := resolveSource(resAttrs, dp.GetAttributes())
	tsMS := nanoToMS(dp.GetTimeUnixNano())

	// Shared attribute labels (without __name__/le) computed once.
	base := map[string]string{
		"org_id": t.OrgID, "project_id": t.ProjectID, "environment": t.Environment,
		"monitoring_app": t.MonitoringApp, "source": source,
	}
	added := mergeAttrLabels(base, resAttrs, maxLabels, 0)
	mergeAttrLabels(base, dp.GetAttributes(), maxLabels, added)

	cloneWith := func(extra map[string]string) map[string]string {
		out := make(map[string]string, len(base)+len(extra))
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	bounds := dp.GetExplicitBounds()
	counts := dp.GetBucketCounts()
	out := make([]prometheus.WriteSeries, 0, len(counts)+2)

	var cumulative uint64
	for i, c := range counts {
		cumulative += c
		var le string
		if i < len(bounds) {
			le = strconv.FormatFloat(bounds[i], 'g', -1, 64)
		} else {
			le = "+Inf" // overflow bucket
		}
		lbls := cloneWith(map[string]string{"__name__": name + "_bucket", "le": le})
		out = append(out, prometheus.WriteSeries{Labels: lbls, Value: float64(cumulative), TimestampMS: tsMS})
	}
	// _sum (only when present) and _count.
	if dp.Sum != nil {
		out = append(out, prometheus.WriteSeries{
			Labels: cloneWith(map[string]string{"__name__": name + "_sum"}), Value: dp.GetSum(), TimestampMS: tsMS,
		})
	}
	out = append(out, prometheus.WriteSeries{
		Labels: cloneWith(map[string]string{"__name__": name + "_count"}), Value: float64(dp.GetCount()), TimestampMS: tsMS,
	})
	return out
}

// LogsToAppLogs flattens an OTLP logs export into AppLog documents with
// authoritative tenancy. body -> Message, severity_text|number -> Level (upper),
// device identity (resolveSource) -> Source, time_unix_nano -> Timestamp,
// attributes -> Fields.
func LogsToAppLogs(req *logsv1.LogsData, t Tenant) []logsearch.AppLog {
	var out []logsearch.AppLog
	for _, rl := range req.GetResourceLogs() {
		resAttrs := rl.GetResource().GetAttributes()
		for _, sl := range rl.GetScopeLogs() {
			for _, lr := range sl.GetLogRecords() {
				source := resolveSource(resAttrs, lr.GetAttributes())
				ts := time.Now()
				if n := lr.GetTimeUnixNano(); n != 0 {
					ts = time.Unix(0, int64(n)).UTC()
				}
				out = append(out, logsearch.AppLog{
					Timestamp:     ts,
					Source:        source,
					Level:         logLevel(lr),
					Message:       anyValueString(lr.GetBody()),
					OrgID:         t.OrgID,
					ProjectID:     t.ProjectID,
					Environment:   t.Environment,
					MonitoringApp: t.MonitoringApp,
				})
			}
		}
	}
	return out
}

// logLevel derives an upper-case level from severity_text, falling back to the
// severity_number's standard band when text is absent.
func logLevel(lr *logsv1.LogRecord) string {
	if txt := strings.TrimSpace(lr.GetSeverityText()); txt != "" {
		return strings.ToUpper(txt)
	}
	switch n := lr.GetSeverityNumber(); {
	case n >= logsv1.SeverityNumber_SEVERITY_NUMBER_FATAL:
		return "FATAL"
	case n >= logsv1.SeverityNumber_SEVERITY_NUMBER_ERROR:
		return "ERROR"
	case n >= logsv1.SeverityNumber_SEVERITY_NUMBER_WARN:
		return "WARN"
	case n >= logsv1.SeverityNumber_SEVERITY_NUMBER_INFO:
		return "INFO"
	case n >= logsv1.SeverityNumber_SEVERITY_NUMBER_DEBUG:
		return "DEBUG"
	case n >= logsv1.SeverityNumber_SEVERITY_NUMBER_TRACE:
		return "TRACE"
	default:
		return ""
	}
}
