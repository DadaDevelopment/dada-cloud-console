package langfuse

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sampleBatch() []Event {
	return []Event{
		{ID: "e1", Type: EventTypeTraceCreate, Timestamp: FormatTime(time.Now()), Body: TraceBody{ID: "t1", Name: "agent-chat-turn"}},
		{ID: "e2", Type: EventTypeObservationCreate, Timestamp: FormatTime(time.Now()), Body: ObservationBody{ID: "o1", TraceID: "t1", Type: ObservationTypeGeneration}},
	}
}

func TestIngestSendsBasicAuthAndOTLPSpans(t *testing.T) {
	type capture struct {
		user, pass string
		ok         bool
		path       string
		version    string
		body       map[string]any
	}
	got := make(chan capture, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		raw, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(raw, &parsed)
		got <- capture{user: u, pass: p, ok: ok, path: r.URL.Path, version: r.Header.Get(ingestionVersionHeader), body: parsed}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"partialSuccess":{}}`))
	}))
	defer srv.Close()

	c := New(srv.URL+"/", "pk-lf-test", "sk-lf-test", true)
	if !c.Configured() {
		t.Fatal("client should be configured")
	}
	if err := c.Ingest(context.Background(), sampleBatch()); err != nil {
		t.Fatalf("Ingest returned %v, want nil", err)
	}

	c1 := <-got
	if !c1.ok || c1.user != "pk-lf-test" || c1.pass != "sk-lf-test" {
		t.Fatalf("basic auth = (%q,%q,%v), want (pk-lf-test,sk-lf-test,true)", c1.user, c1.pass, c1.ok)
	}
	if c1.path != ingestPath {
		t.Fatalf("path = %q, want %q (trailing slash on host must be trimmed)", c1.path, ingestPath)
	}
	if c1.version != ingestionVersion {
		t.Fatalf("%s = %q, want %q", ingestionVersionHeader, c1.version, ingestionVersion)
	}

	spans := otlpSpans(t, c1.body)
	if len(spans) != 2 {
		t.Fatalf("span count = %d, want 2", len(spans))
	}
	root, child := spans[0], spans[1]
	if root["parentSpanId"] != nil {
		t.Fatalf("trace-create must become the root span, got parent %v", root["parentSpanId"])
	}
	if root["traceId"] != child["traceId"] {
		t.Fatalf("observation landed in another trace: %v vs %v", root["traceId"], child["traceId"])
	}
	if child["parentSpanId"] != root["spanId"] {
		t.Fatalf("observation without a parent must hang off the root span: parent=%v root=%v", child["parentSpanId"], root["spanId"])
	}
	if got := otlpAttr(root, "langfuse.trace.name"); got != "agent-chat-turn" {
		t.Fatalf("langfuse.trace.name = %q, want agent-chat-turn", got)
	}
	if got := otlpAttr(child, "langfuse.observation.type"); got != "generation" {
		t.Fatalf("langfuse.observation.type = %q, want generation", got)
	}
	for _, span := range spans {
		for _, key := range []string{"traceId", "spanId", "name", "startTimeUnixNano", "endTimeUnixNano"} {
			if s, _ := span[key].(string); s == "" {
				t.Fatalf("span %v lacks %s", span["name"], key)
			}
		}
	}
}

func TestIngestJoinsObservationsAcrossBatches(t *testing.T) {
	first, err := otlpPayload(sampleBatch()[:1])
	if err != nil {
		t.Fatal(err)
	}
	second, err := otlpPayload(sampleBatch()[1:])
	if err != nil {
		t.Fatal(err)
	}
	root := otlpSpans(t, first)[0]
	child := otlpSpans(t, second)[0]
	if root["traceId"] != child["traceId"] || child["parentSpanId"] != root["spanId"] {
		t.Fatalf("a trace and its observation sent separately must still join: root=%v child=%v", root, child)
	}
}

func TestIngestRejectsUnknownEventBody(t *testing.T) {
	_, err := otlpPayload([]Event{{ID: "e1", Type: EventTypeTraceCreate, Body: "nope"}})
	if err == nil {
		t.Fatal("an event body of an unknown type must be an error, not a silently dropped span")
	}
	if !strings.Contains(err.Error(), "e1") {
		t.Fatalf("error should name the event, got %v", err)
	}
}

func otlpSpans(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		ResourceSpans []struct {
			ScopeSpans []struct {
				Spans []map[string]any `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	var spans []map[string]any
	for _, rs := range parsed.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			spans = append(spans, ss.Spans...)
		}
	}
	if len(spans) == 0 {
		t.Fatalf("request body carries no spans: %v", body)
	}
	return spans
}

func otlpAttr(span map[string]any, key string) string {
	attrs, _ := span["attributes"].([]any)
	for _, a := range attrs {
		kv, _ := a.(map[string]any)
		if kv["key"] != key {
			continue
		}
		value, _ := kv["value"].(map[string]any)
		s, _ := value["stringValue"].(string)
		return s
	}
	return ""
}

func TestIngestReturnsErrorOnHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"unauthorized"}`))
	}))
	defer srv.Close()

	err := New(srv.URL, "pk", "sk", true).Ingest(context.Background(), sampleBatch())
	if err == nil {
		t.Fatal("expected an error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error should mention the status, got %v", err)
	}
}

func TestIngestSucceedsOnUnparseableSuccessBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	if err := New(srv.URL, "pk", "sk", true).Ingest(context.Background(), sampleBatch()); err != nil {
		t.Fatalf("a 2xx with a non-JSON body must not be an error, got %v", err)
	}
}

func TestIngestNoOpWhenNotConfigured(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cases := map[string]*Client{
		"no host":      New("", "pk", "sk", true),
		"no keys":      New(srv.URL, "", "", true),
		"no secret":    New(srv.URL, "pk", "", true),
		"disabled":     New(srv.URL, "pk", "sk", false),
		"nil receiver": nil,
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if c.Configured() {
				t.Fatal("client should not be configured")
			}
			if err := c.Ingest(context.Background(), sampleBatch()); err != nil {
				t.Fatalf("Ingest on an unconfigured client returned %v, want nil", err)
			}
			c.IngestAsync(sampleBatch())
		})
	}

	time.Sleep(100 * time.Millisecond)
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("server was hit %d time(s) by unconfigured clients, want 0", n)
	}
}

func TestIngestNoOpOnEmptyBatch(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "pk", "sk", true)
	if err := c.Ingest(context.Background(), nil); err != nil {
		t.Fatalf("empty batch returned %v, want nil", err)
	}
	c.IngestAsync(nil)
	time.Sleep(100 * time.Millisecond)
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("server was hit %d time(s) for an empty batch, want 0", n)
	}
}

func TestIngestAsyncNeverPanics(t *testing.T) {
	reached := make(chan struct{}, 4)

	panicking := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached <- struct{}{}
		panic("boom")
	}))
	defer panicking.Close()
	panicking.Config.ErrorLog = nil

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached <- struct{}{}
		time.Sleep(ingestTimeout + time.Second)
	}))
	defer slow.Close()

	New(panicking.URL, "pk", "sk", true).IngestAsync(sampleBatch())
	New(slow.URL, "pk", "sk", true).IngestAsync(sampleBatch())

	for i := 0; i < 2; i++ {
		select {
		case <-reached:
		case <-time.After(3 * time.Second):
			t.Fatal("server was never reached")
		}
	}
}

func TestIngestAsyncDoesNotBlockCaller(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer slow.Close()

	start := time.Now()
	New(slow.URL, "pk", "sk", true).IngestAsync(sampleBatch())
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("IngestAsync blocked for %v, must return immediately", elapsed)
	}
}

func TestFormatTimeHasMilliseconds(t *testing.T) {
	got := FormatTime(time.Date(2026, 8, 2, 10, 30, 15, 123000000, time.UTC))
	if !strings.HasSuffix(got, "Z") {
		t.Fatalf("timestamp should be UTC with a Z suffix, got %q", got)
	}
	if !strings.Contains(got, ".123") {
		t.Fatalf("timestamp should keep millisecond resolution, got %q", got)
	}

	zero := FormatTime(time.Date(2026, 8, 2, 10, 30, 15, 0, time.UTC))
	if !strings.Contains(zero, ".000") {
		t.Fatalf("millisecond field must not be elided, got %q", zero)
	}
}

func TestIngestStampsTracingEnvironmentOnEverySpan(t *testing.T) {
	t.Setenv("LANGFUSE_TRACING_ENVIRONMENT", "prod")
	body, err := otlpPayload(sampleBatch())
	if err != nil {
		t.Fatal(err)
	}
	for _, span := range otlpSpans(t, body) {
		if got := otlpAttr(span, "langfuse.environment"); got != "prod" {
			t.Fatalf("span %v must carry the process tracing environment, got %q", span["name"], got)
		}
	}

	t.Setenv("LANGFUSE_TRACING_ENVIRONMENT", "")
	body, err = otlpPayload(sampleBatch())
	if err != nil {
		t.Fatal(err)
	}
	if got := otlpAttr(otlpSpans(t, body)[0], "langfuse.environment"); got != "default" {
		t.Fatalf("without the variable the environment must be Langfuse's default, got %q", got)
	}
}
