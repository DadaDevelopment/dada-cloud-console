package api

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAwaitPublishedURL_WaitsForTheEdgeToProgram(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello-dada"))
	}))
	defer srv.Close()

	got := awaitPublishedURL(srv.URL, "box.example", 5*time.Second, 10*time.Millisecond)
	if !got.ok || got.status != http.StatusOK {
		t.Fatalf("probe = %+v, want ok 200 once the edge answers", got)
	}
	if got.attempts != 3 {
		t.Errorf("attempts = %d, want 3 (two failures then the real 200)", got.attempts)
	}
	if got.body != "hello-dada" {
		t.Errorf("body = %q, want the body of the successful response", got.body)
	}
}

func TestAwaitPublishedURL_BudgetExhaustedIsAFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	started := time.Now()
	got := awaitPublishedURL(srv.URL, "box.example", 60*time.Millisecond, 10*time.Millisecond)
	if got.ok {
		t.Fatalf("probe = %+v, want failure when nothing ever answers 200", got)
	}
	if got.status != http.StatusBadGateway {
		t.Errorf("status = %d, want the last observed status reported honestly", got.status)
	}
	if got.attempts < 2 {
		t.Errorf("attempts = %d, want more than one attempt inside the budget", got.attempts)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("elapsed = %s, want the probe to stop at its budget", elapsed)
	}
}
