package jenkins

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStopBuildHitsStopEndpoint proves StopBuild issues a POST against the
// job's own /<number>/stop endpoint (the same nested job/<name>/job/<name>
// path scheme every other client call uses), and tolerates the crumb-issuer
// 404 the same way TriggerBuild already does.
func TestStopBuildHitsStopEndpoint(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/crumbIssuer/api/json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "t")
	if err := c.StopBuild(context.Background(), "web", 42); err != nil {
		t.Fatalf("StopBuild: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/job/web/42/stop" {
		t.Errorf("path = %q, want /job/web/42/stop", gotPath)
	}
}

// TestStopBuildNotFoundIsNotAnError proves the idempotency contract: a build
// that already finished, or whose job/number no longer exists, must not fail
// the caller -- the state StopBuild was asked to reach (no live Jenkins job)
// already holds.
func TestStopBuildNotFoundIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/crumbIssuer/api/json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "t")
	if err := c.StopBuild(context.Background(), "web", 42); err != nil {
		t.Fatalf("StopBuild on a 404 must not be an error, got %v", err)
	}
}

// TestStopBuildConflictIsNotAnError covers a build already stopped racing a
// second stop call (e.g. bridge's periodic check firing twice): Jenkins can
// answer 409 for a build no longer stoppable, which is again the state being
// asked for, not a failure.
func TestStopBuildConflictIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/crumbIssuer/api/json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "t")
	if err := c.StopBuild(context.Background(), "web", 42); err != nil {
		t.Fatalf("StopBuild on a 409 must not be an error, got %v", err)
	}
}

// TestStopBuildServerErrorIsAnError proves StopBuild does not silently
// swallow a real upstream failure alongside the tolerated 404/409.
func TestStopBuildServerErrorIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/crumbIssuer/api/json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "t")
	c.attempts = 1
	if err := c.StopBuild(context.Background(), "web", 42); err == nil {
		t.Fatal("want an error on a genuine 500")
	}
}

// TestCancelQueueItemHitsCancelEndpoint proves CancelQueueItem posts to
// queue/cancelItem with the queue id as a query parameter, per the stock
// Jenkins Remote API (no plugin required, matching every other call in this
// client).
func TestCancelQueueItemHitsCancelEndpoint(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/crumbIssuer/api/json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "t")
	if err := c.CancelQueueItem(context.Background(), 67584); err != nil {
		t.Fatalf("CancelQueueItem: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/queue/cancelItem" {
		t.Errorf("path = %q, want /queue/cancelItem", gotPath)
	}
	if gotQuery != "id=67584" {
		t.Errorf("query = %q, want id=67584", gotQuery)
	}
}

// TestCancelQueueItemNotFoundIsNotAnError mirrors StopBuild's tolerance: an
// item Jenkins no longer holds in the queue (started, already canceled, or
// evicted) already means "not queued any more", which is the outcome being
// asked for.
func TestCancelQueueItemNotFoundIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/crumbIssuer/api/json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "t")
	if err := c.CancelQueueItem(context.Background(), 5); err != nil {
		t.Fatalf("CancelQueueItem on a 404 must not be an error, got %v", err)
	}
}
