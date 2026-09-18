package langfuse

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// otlpPayload renders a legacy-shaped event batch as one OTLP/JSON
// ExportTraceServiceRequest the Langfuse OTLP endpoint accepts.
//
// A trace-create event becomes the root span of its trace and carries the
// trace-level attributes (name, user, session, tags, metadata); Langfuse v4
// derives the trace from its root observation, so a trace without a root span
// in the batch would show up with nothing but children. An observation-create
// event becomes a child span; one without a parent is parented to the root
// span so the tree stays one tree the way the batch API nested it. Ids are
// derived deterministically from the event ids, which keeps a trace and its
// observations joinable when they arrive in different batches (the transcript
// store writes single-trace batches, the turn recorder writes a whole turn).
func otlpPayload(batch []Event) (map[string]any, error) {
	environment := tracingEnvironment()
	spans := make([]map[string]any, 0, len(batch))
	roots := map[string]map[string]any{}
	rootStart := map[string]time.Time{}
	latestEnd := map[string]time.Time{}

	for _, ev := range batch {
		switch body := ev.Body.(type) {
		case TraceBody:
			span, start, err := traceSpan(body, ev.Timestamp)
			if err != nil {
				return nil, err
			}
			roots[body.ID], rootStart[body.ID] = span, start
			spans = append(spans, span)
		case *TraceBody:
			span, start, err := traceSpan(*body, ev.Timestamp)
			if err != nil {
				return nil, err
			}
			roots[body.ID], rootStart[body.ID] = span, start
			spans = append(spans, span)
		case ObservationBody:
			span, end, err := observationSpan(body, ev.Timestamp)
			if err != nil {
				return nil, err
			}
			if end.After(latestEnd[body.TraceID]) {
				latestEnd[body.TraceID] = end
			}
			spans = append(spans, span)
		case *ObservationBody:
			span, end, err := observationSpan(*body, ev.Timestamp)
			if err != nil {
				return nil, err
			}
			if end.After(latestEnd[body.TraceID]) {
				latestEnd[body.TraceID] = end
			}
			spans = append(spans, span)
		default:
			return nil, fmt.Errorf("event %s: unsupported body %T", ev.ID, ev.Body)
		}
	}

	for traceID, root := range roots {
		if end, ok := latestEnd[traceID]; ok && end.After(rootStart[traceID]) {
			root["endTimeUnixNano"] = unixNano(end)
		}
	}

	return map[string]any{
		"resourceSpans": []map[string]any{{
			"resource": map[string]any{"attributes": []map[string]any{
				attr("service.name", "dada-console"),
				attr("langfuse.environment", environment),
				attr("deployment.environment.name", environment),
			}},
			"scopeSpans": []map[string]any{{
				"scope": map[string]any{"name": "dada-console/langfuse"},
				"spans": spans,
			}},
		}},
	}, nil
}

// tracingEnvironment is the Langfuse environment every span of this process
// lands in. It reads the same LANGFUSE_TRACING_ENVIRONMENT the kagent-app
// image reads, so a console-emitted trace and an agent-emitted trace of the
// same deployment sit in the same environment instead of the Go side falling
// into "default" next to the agents' "prod".
func tracingEnvironment() string {
	if v := strings.TrimSpace(os.Getenv("LANGFUSE_TRACING_ENVIRONMENT")); v != "" {
		return v
	}
	return "default"
}

func traceSpan(body TraceBody, eventTimestamp string) (map[string]any, time.Time, error) {
	traceID, err := otlpTraceID(body.ID)
	if err != nil {
		return nil, time.Time{}, err
	}
	start := parseTime(body.Timestamp, eventTimestamp)
	if start.IsZero() {
		start = time.Now()
	}
	attrs := []map[string]any{
		attr("langfuse.observation.type", "span"),
		attr("langfuse.environment", tracingEnvironment()),
	}
	if body.Name != "" {
		attrs = append(attrs, attr("langfuse.trace.name", body.Name))
	}
	if body.UserID != "" {
		attrs = append(attrs, attr("langfuse.user.id", body.UserID))
	}
	if body.SessionID != "" {
		attrs = append(attrs, attr("langfuse.session.id", body.SessionID))
	}
	if len(body.Tags) > 0 {
		attrs = append(attrs, attr("langfuse.trace.tags", body.Tags))
	}
	if body.Input != nil {
		attrs = append(attrs, attr("langfuse.trace.input", body.Input), attr("langfuse.observation.input", body.Input))
	}
	if body.Output != nil {
		attrs = append(attrs, attr("langfuse.trace.output", body.Output), attr("langfuse.observation.output", body.Output))
	}
	attrs = append(attrs, metadataAttrs("langfuse.trace.metadata.", body.Metadata)...)
	attrs = append(attrs, metadataAttrs("langfuse.observation.metadata.", body.Metadata)...)

	name := body.Name
	if name == "" {
		name = "trace"
	}
	return map[string]any{
		"traceId":           traceID,
		"spanId":            rootSpanID(body.ID),
		"name":              name,
		"kind":              1,
		"startTimeUnixNano": unixNano(start),
		"endTimeUnixNano":   unixNano(start),
		"attributes":        attrs,
	}, start, nil
}

func observationSpan(body ObservationBody, eventTimestamp string) (map[string]any, time.Time, error) {
	traceID, err := otlpTraceID(body.TraceID)
	if err != nil {
		return nil, time.Time{}, err
	}
	start := parseTime(body.StartTime, eventTimestamp)
	if start.IsZero() {
		start = time.Now()
	}
	end := parseTime(body.EndTime, "")
	if end.IsZero() || end.Before(start) {
		end = start
	}
	parent := rootSpanID(body.TraceID)
	if body.ParentObservationID != "" {
		parent = spanID(body.ParentObservationID)
	}
	attrs := []map[string]any{
		attr("langfuse.observation.type", strings.ToLower(body.Type)),
		attr("langfuse.environment", tracingEnvironment()),
	}
	if body.Input != nil {
		attrs = append(attrs, attr("langfuse.observation.input", body.Input))
	}
	if body.Output != nil {
		attrs = append(attrs, attr("langfuse.observation.output", body.Output))
	}
	if body.Model != "" {
		attrs = append(attrs, attr("langfuse.observation.model.name", body.Model))
	}
	if body.Usage != nil {
		if body.Usage.PromptTokens > 0 {
			attrs = append(attrs, attr("langfuse.observation.usage_details.input", body.Usage.PromptTokens))
		}
		if body.Usage.CompletionTokens > 0 {
			attrs = append(attrs, attr("langfuse.observation.usage_details.output", body.Usage.CompletionTokens))
		}
		if body.Usage.TotalTokens > 0 {
			attrs = append(attrs, attr("langfuse.observation.usage_details.total", body.Usage.TotalTokens))
		}
	}
	if body.Level != "" {
		attrs = append(attrs, attr("langfuse.observation.level", body.Level))
	}
	if body.StatusMessage != "" {
		attrs = append(attrs, attr("langfuse.observation.status_message", body.StatusMessage))
	}
	attrs = append(attrs, metadataAttrs("langfuse.observation.metadata.", body.Metadata)...)

	name := body.Name
	if name == "" {
		name = strings.ToLower(body.Type)
	}
	span := map[string]any{
		"traceId":           traceID,
		"spanId":            spanID(body.ID),
		"parentSpanId":      parent,
		"name":              name,
		"kind":              1,
		"startTimeUnixNano": unixNano(start),
		"endTimeUnixNano":   unixNano(end),
		"attributes":        attrs,
	}
	if body.Level == LevelError {
		span["status"] = map[string]any{"code": 2, "message": body.StatusMessage}
	}
	return span, end, nil
}

// otlpTraceID turns a caller's trace id into the 16-byte hex OTLP wants. A
// UUID (the shape every caller uses) maps byte for byte, so the trace keeps
// the id the console already logged; anything else is hashed down.
func otlpTraceID(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("trace id is empty")
	}
	compact := strings.ToLower(strings.ReplaceAll(id, "-", ""))
	if len(compact) == 32 {
		if _, err := hex.DecodeString(compact); err == nil {
			return compact, nil
		}
	}
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:16]), nil
}

func rootSpanID(traceID string) string {
	return spanID("trace-root:" + traceID)
}

func spanID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:8])
}

func parseTime(primary, fallback string) time.Time {
	for _, raw := range []string{primary, fallback} {
		if raw == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}

func unixNano(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return strconv.FormatInt(t.UnixNano(), 10)
}

func metadataAttrs(prefix string, metadata map[string]any) []map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	keys := make([]string, 0, len(metadata))
	for k := range metadata {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, attr(prefix+k, metadata[k]))
	}
	return out
}

// attr renders one OTLP KeyValue. Scalars keep their OTLP type; everything
// else is serialised to JSON text, which Langfuse parses back for the
// input/output/metadata attributes and keeps as text elsewhere.
func attr(key string, value any) map[string]any {
	return map[string]any{"key": key, "value": anyValue(value)}
}

func anyValue(value any) map[string]any {
	switch v := value.(type) {
	case nil:
		return map[string]any{"stringValue": ""}
	case string:
		return map[string]any{"stringValue": v}
	case bool:
		return map[string]any{"boolValue": v}
	case int:
		return map[string]any{"intValue": strconv.Itoa(v)}
	case int32:
		return map[string]any{"intValue": strconv.FormatInt(int64(v), 10)}
	case int64:
		return map[string]any{"intValue": strconv.FormatInt(v, 10)}
	case float32:
		return map[string]any{"doubleValue": float64(v)}
	case float64:
		return map[string]any{"doubleValue": v}
	case []string:
		values := make([]map[string]any, 0, len(v))
		for _, s := range v {
			values = append(values, map[string]any{"stringValue": s})
		}
		return map[string]any{"arrayValue": map[string]any{"values": values}}
	case json.RawMessage:
		return map[string]any{"stringValue": string(v)}
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return map[string]any{"stringValue": fmt.Sprint(v)}
		}
		return map[string]any{"stringValue": string(raw)}
	}
}
