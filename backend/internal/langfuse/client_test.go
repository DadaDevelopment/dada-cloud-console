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

func TestIngestSendsBasicAuthAndBatch(t *testing.T) {
	type capture struct {
		user, pass string
		ok         bool
		path       string
		body       map[string]any
	}
	got := make(chan capture, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		raw, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(raw, &parsed)
		got <- capture{user: u, pass: p, ok: ok, path: r.URL.Path, body: parsed}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`{"successes":[{"id":"e1","status":201},{"id":"e2","status":201}],"errors":[]}`))
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

	rawBatch, ok := c1.body["batch"].([]any)
	if !ok {
		t.Fatalf("request body has no batch array: %v", c1.body)
	}
	if len(rawBatch) != 2 {
		t.Fatalf("batch length = %d, want 2", len(rawBatch))
	}
	wantTypes := []string{EventTypeTraceCreate, EventTypeObservationCreate}
	for i, item := range rawBatch {
		ev, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("batch[%d] is not an object", i)
		}
		if ev["type"] != wantTypes[i] {
			t.Fatalf("batch[%d].type = %v, want %v", i, ev["type"], wantTypes[i])
		}
		if s, _ := ev["id"].(string); s == "" {
			t.Fatalf("batch[%d].id is empty", i)
		}
		if s, _ := ev["timestamp"].(string); s == "" {
			t.Fatalf("batch[%d].timestamp is empty", i)
		}
		if _, ok := ev["body"]; !ok {
			t.Fatalf("batch[%d] has no body", i)
		}
	}
}

func TestIngestReturnsErrorOnRejectedEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`{"successes":[],"errors":[{"id":"e1","status":400,"message":"invalid trace body"}]}`))
	}))
	defer srv.Close()

	err := New(srv.URL, "pk", "sk", true).Ingest(context.Background(), sampleBatch())
	if err == nil {
		t.Fatal("expected an error when the server rejects events")
	}
	if !strings.Contains(err.Error(), "invalid trace body") {
		t.Fatalf("error should carry the rejection reason, got %v", err)
	}
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
