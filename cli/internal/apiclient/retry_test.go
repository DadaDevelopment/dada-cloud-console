package apiclient

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestRetriesConnectionsThatDiedBeforeTheRequest reproduces the failure in the
// owner's screenshot: the first attempt dies during the handshake, before any
// request byte reaches the console, and the deploy used to end right there.
func TestRetriesConnectionsThatDiedBeforeTheRequest(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"projects": []Project{{ID: "p1", Name: "demo"}}})
	}))
	defer srv.Close()

	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if atomic.AddInt32(&attempts, 1) == 1 {
				return nil, &net.OpError{Op: "dial", Err: errHandshakeTimeout{}}
			}
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}

	c := New(srv.URL, hc, nil, "")
	projects, err := c.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("expected the retry to succeed, got %v", err)
	}
	if len(projects) != 1 || projects[0].ID != "p1" {
		t.Fatalf("got %+v", projects)
	}
	if got := atomic.LoadInt32(&attempts); got < 2 {
		t.Fatalf("expected a second attempt, saw %d connections", got)
	}
}

type errHandshakeTimeout struct{}

func (errHandshakeTimeout) Error() string { return "net/http: TLS handshake timeout" }
func (errHandshakeTimeout) Timeout() bool { return true }

// TestDoesNotRetryOnceTheServerHasTheRequest keeps the retry honest: a request
// the console already received must never be sent twice, or a deploy could
// queue two builds.
func TestDoesNotRetryOnceTheServerHasTheRequest(t *testing.T) {
	var hits int32
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("test server does not support hijacking")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	})
	defer srv.Close()

	if _, err := c.ListProjects(context.Background()); err == nil {
		t.Fatal("expected an error when the console drops the response")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("request reached the console %d times, want exactly 1", got)
	}
}
