package tggateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func authProbe(t *testing.T, srv *Server, method, path, auth string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader("{"))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec.Code
}

func TestInternalRoutesRequireBearer(t *testing.T) {
	srv := NewServer(nil)
	srv.SetToken("s3cret")
	routes := []struct{ method, path string }{
		{http.MethodPost, "/bindings"},
		{http.MethodPost, "/outbound"},
		{http.MethodGet, "/bindings/a"},
		{http.MethodDelete, "/bindings/a"},
	}
	for _, r := range routes {
		if got := authProbe(t, srv, r.method, r.path, ""); got != http.StatusUnauthorized {
			t.Errorf("%s %s without token: got %d, want 401", r.method, r.path, got)
		}
		if got := authProbe(t, srv, r.method, r.path, "Bearer wrong"); got != http.StatusUnauthorized {
			t.Errorf("%s %s with wrong token: got %d, want 401", r.method, r.path, got)
		}
		if got := authProbe(t, srv, r.method, r.path, "s3cret"); got != http.StatusUnauthorized {
			t.Errorf("%s %s without Bearer prefix: got %d, want 401", r.method, r.path, got)
		}
	}
	for _, r := range routes[:2] {
		if got := authProbe(t, srv, r.method, r.path, "Bearer s3cret"); got != http.StatusBadRequest {
			t.Errorf("%s %s with right token and bad json: got %d, want 400 from the handler", r.method, r.path, got)
		}
	}
}

func TestRightTokenReaches200(t *testing.T) {
	store := newFakeStore()
	if err := store.Upsert(context.Background(), Binding{AgentName: "a", BotToken: "tok", BotUsername: "a_bot", Status: StatusActive}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	srv := NewServer(NewManager(store, fakeTelegram{}, fakeA2A{}, nil))
	srv.SetToken("s3cret")
	if got := authProbe(t, srv, http.MethodGet, "/bindings/a", "Bearer s3cret"); got != http.StatusOK {
		t.Errorf("GET /bindings/a with right token: got %d, want 200", got)
	}
	if got := authProbe(t, srv, http.MethodGet, "/bindings/a", "Bearer s3creT"); got != http.StatusUnauthorized {
		t.Errorf("GET /bindings/a with near-miss token: got %d, want 401", got)
	}
	if got := authProbe(t, srv, http.MethodDelete, "/bindings/a", "Bearer s3cret"); got != http.StatusOK {
		t.Errorf("DELETE /bindings/a with right token: got %d, want 200", got)
	}
}

func TestUnsetTokenFailsClosed(t *testing.T) {
	srv := NewServer(nil)
	if got := authProbe(t, srv, http.MethodPost, "/outbound", "Bearer "); got != http.StatusServiceUnavailable {
		t.Errorf("unset token: got %d, want 503", got)
	}
	if got := authProbe(t, srv, http.MethodPost, "/outbound", ""); got != http.StatusServiceUnavailable {
		t.Errorf("unset token no header: got %d, want 503", got)
	}
}

func TestProbesStayOpen(t *testing.T) {
	srv := NewServer(nil)
	srv.SetToken("s3cret")
	for _, p := range []string{"/healthz", "/readyz"} {
		if got := authProbe(t, srv, http.MethodGet, p, ""); got != http.StatusOK {
			t.Errorf("%s: got %d, want 200", p, got)
		}
	}
}
