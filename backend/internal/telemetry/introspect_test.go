package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A fresh cache entry is served without a second network call (the hot ingest
// path must not hit user-service per request).
func TestIntrospectCachesByKeyHash(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"valid":true,"principal_id":"p1","scopes":["metrics:write"],"project_id":"proj","org_id":"org","monitoring_app":"app"}`))
	}))
	defer srv.Close()

	in := NewIntrospector(srv.URL)
	for i := 0; i < 3; i++ {
		res, err := in.Introspect(context.Background(), "sk-dada-abc")
		if err != nil {
			t.Fatalf("introspect: %v", err)
		}
		if !res.Valid || res.ProjectID != "proj" || res.OrgID != "org" || res.MonitoringApp != "app" {
			t.Fatalf("unexpected result: %+v", res)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("user-service calls = %d, want 1 (cache miss only once)", got)
	}
}

// When user-service is unreachable but a cached entry exists, serve it (do not
// fail an otherwise-valid key on a transient outage).
func TestIntrospectServesStaleOnOutage(t *testing.T) {
	var up atomic.Bool
	up.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !up.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"valid":true,"principal_id":"p1","project_id":"proj"}`))
	}))
	defer srv.Close()

	in := NewIntrospector(srv.URL, WithIntrospectTTL(time.Nanosecond))
	if _, err := in.Introspect(context.Background(), "sk-dada-abc"); err != nil {
		t.Fatalf("warm cache: %v", err)
	}
	up.Store(false)
	time.Sleep(time.Millisecond)
	res, err := in.Introspect(context.Background(), "sk-dada-abc")
	if err != nil {
		t.Fatalf("expected stale cache hit, got error: %v", err)
	}
	if res.ProjectID != "proj" {
		t.Fatalf("stale result lost: %+v", res)
	}
}

// No cached entry + user-service unreachable -> fail closed (error).
func TestIntrospectFailsClosedWhenUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	in := NewIntrospector(srv.URL)
	if _, err := in.Introspect(context.Background(), "sk-dada-xyz"); err == nil {
		t.Error("expected error on unreachable user-service with no cache, got nil")
	}
}

// Empty base URL yields no introspector (unified keys then unconfigured).
func TestNewIntrospectorNilWhenUnconfigured(t *testing.T) {
	if NewIntrospector("") != nil {
		t.Error("expected nil introspector for empty base URL")
	}
}
