package grafana

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBasicAuthClientSendsBasicAuth verifies the durable auth path: a client
// built with NewBasicAuth authenticates requests with HTTP Basic (admin
// credentials), never a Bearer token. This is what survives the emptyDir
// Grafana DB wipe that invalidates service-account tokens.
func TestBasicAuthClientSendsBasicAuth(t *testing.T) {
	var gotUser, gotPass string
	var gotOK bool
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, gotOK = r.BasicAuth()
		gotAuthHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewBasicAuth(srv.URL, "admin", "s3cret", "prom", "")
	if c == nil {
		t.Fatal("NewBasicAuth returned nil for valid config")
	}
	if _, err := c.do(context.Background(), http.MethodGet, "/api/folders", nil, nil, false); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if !gotOK || gotUser != "admin" || gotPass != "s3cret" {
		t.Fatalf("expected basic auth admin/s3cret, got ok=%v user=%q pass=%q", gotOK, gotUser, gotPass)
	}
	if got := gotAuthHeader[:6]; got != "Basic " {
		t.Fatalf("expected Basic authorization scheme, got %q", gotAuthHeader)
	}
}

// TestTokenClientSendsBearer verifies the legacy token path still sends a Bearer
// header (used where Grafana has persistent storage).
func TestTokenClientSendsBearer(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok123", "prom", "")
	if c == nil {
		t.Fatal("New returned nil for valid config")
	}
	if _, err := c.do(context.Background(), http.MethodGet, "/api/folders", nil, nil, false); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if gotAuth != "Bearer tok123" {
		t.Fatalf("expected Bearer tok123, got %q", gotAuth)
	}
}

// TestConstructorsNilOnMissingConfig confirms both constructors return nil when
// required fields are absent, so callers disable provisioning (503) cleanly.
func TestConstructorsNilOnMissingConfig(t *testing.T) {
	if New("", "tok", "prom", "") != nil {
		t.Fatal("New must be nil without baseURL")
	}
	if New("http://x", "", "prom", "") != nil {
		t.Fatal("New must be nil without token")
	}
	if NewBasicAuth("http://x", "", "p", "prom", "") != nil {
		t.Fatal("NewBasicAuth must be nil without user")
	}
	if NewBasicAuth("http://x", "u", "", "prom", "") != nil {
		t.Fatal("NewBasicAuth must be nil without pass")
	}
}
