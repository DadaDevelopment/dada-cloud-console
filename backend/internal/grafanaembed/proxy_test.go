package grafanaembed

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// upstream captures what Grafana would see.
type seen struct {
	user, email, groups string
	host                string
	gotEmbedToken       bool
}

func newProxy(t *testing.T, capture *seen) (*Proxy, *httptest.Server) {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.user = r.Header.Get(HeaderUser)
		capture.email = r.Header.Get(HeaderEmail)
		capture.groups = r.Header.Get(HeaderGroups)
		capture.host = r.Host
		capture.gotEmbedToken = r.URL.Query().Get(QueryParam) != ""
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(up.Close)
	p, err := NewProxy(Config{
		UpstreamURL:  up.URL,
		Secret:       secret,
		UpstreamHost: "grafana.dada-tuda.ru",
		SessionTTL:   30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("new proxy: %v", err)
	}
	now := time.Unix(1_700_000_000, 0)
	p.now = func() time.Time { return now }
	return p, up
}

func TestProxyInjectsHeadersFromQueryToken(t *testing.T) {
	var cap seen
	p, _ := newProxy(t, &cap)
	now := time.Unix(1_700_000_000, 0)
	tok, _ := Sign(secret, Claims{User: "alice", Email: "a@x.io", Groups: []string{"proj:a"}, Dashboard: "d1"}, now, 2*time.Minute)

	rw := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/d/d1?kiosk&"+QueryParam+"="+tok, nil)
	p.ServeHTTP(rw, req)

	if cap.user != "alice" || cap.email != "a@x.io" || cap.groups != "proj:a" {
		t.Fatalf("headers not injected: %+v", cap)
	}
	if cap.host != "grafana.dada-tuda.ru" {
		t.Fatalf("upstream host = %q", cap.host)
	}
	if cap.gotEmbedToken {
		t.Fatal("embed_token leaked to upstream")
	}
	// A sliding-session cookie must be set on the first authed hit.
	if !strings.Contains(rw.Header().Get("Set-Cookie"), "dada_grafana_embed=") {
		t.Fatalf("no session cookie set: %q", rw.Header().Get("Set-Cookie"))
	}
	if sc := rw.Header().Get("Set-Cookie"); !strings.Contains(sc, "SameSite=None") || !strings.Contains(sc, "Secure") {
		t.Fatalf("cookie not SameSite=None; Secure: %q", sc)
	}
}

func TestProxyAuthenticatesViaCookieOnSubsequentRequest(t *testing.T) {
	var cap seen
	p, _ := newProxy(t, &cap)
	now := time.Unix(1_700_000_000, 0)
	tok, _ := Sign(secret, Claims{User: "bob", Groups: []string{"proj:b"}}, now, 30*time.Minute)

	rw := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/ds/query", nil) // no query token
	req.AddCookie(&http.Cookie{Name: "dada_grafana_embed", Value: tok})
	p.ServeHTTP(rw, req)

	if cap.user != "bob" || cap.groups != "proj:b" {
		t.Fatalf("cookie auth failed: %+v", cap)
	}
}

func TestProxyPassesThroughUnauthenticated(t *testing.T) {
	var cap seen
	p, _ := newProxy(t, &cap)
	rw := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login", nil) // admin SSO traffic
	p.ServeHTTP(rw, req)

	if cap.user != "" || cap.groups != "" {
		t.Fatalf("must not inject identity for unauthenticated traffic: %+v", cap)
	}
	if rw.Header().Get("Set-Cookie") != "" {
		t.Fatalf("must not set cookie for unauthenticated traffic")
	}
}

func TestProxyStripsClientSuppliedIdentityHeaders(t *testing.T) {
	var cap seen
	p, _ := newProxy(t, &cap)
	rw := httptest.NewRecorder()
	// Attacker tries to spoof identity directly, no token.
	req := httptest.NewRequest(http.MethodGet, "/api/user", nil)
	req.Header.Set(HeaderUser, "admin")
	req.Header.Set(HeaderGroups, "proj:victim")
	p.ServeHTTP(rw, req)

	if cap.user != "" || cap.groups != "" {
		t.Fatalf("spoofed headers reached upstream: %+v", cap)
	}
}

func TestProxyExpiredTokenDoesNotAuthenticate(t *testing.T) {
	var cap seen
	p, _ := newProxy(t, &cap)
	staleNow := time.Unix(1_700_000_000, 0).Add(-time.Hour)
	tok, _ := Sign(secret, Claims{User: "alice"}, staleNow, 2*time.Minute)
	rw := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/d/d1?"+QueryParam+"="+tok, nil)
	p.ServeHTTP(rw, req)
	if cap.user != "" {
		t.Fatalf("expired token authenticated: %+v", cap)
	}
}
