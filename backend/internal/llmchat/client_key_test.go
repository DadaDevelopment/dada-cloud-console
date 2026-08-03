package llmchat

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestKeyFuncWinsOverStaticKey pins the precedence the console's ServiceIdentity
// cutover depends on: the resolved identity token must be what actually reaches
// the gateway, not the static key left in the deployment's environment.
func TestKeyFuncWinsOverStaticKey(t *testing.T) {
	c := New("http://gateway.test", "static-key", "model")
	c.KeyFunc = func() string { return "identity-token" }
	if got := c.key(); got != "identity-token" {
		t.Fatalf("key()=%q want identity-token; the static key would be sent instead of the identity", got)
	}
}

// TestKeyFuncEmptyFallsBackToStatic keeps the pre-identity world working: until
// the first successful resolve, and off-cluster where there is no Secret at all,
// the static key is the only credential and chat must still authenticate.
func TestKeyFuncEmptyFallsBackToStatic(t *testing.T) {
	c := New("http://gateway.test", "static-key", "model")
	c.KeyFunc = func() string { return "" }
	if got := c.key(); got != "static-key" {
		t.Fatalf("key()=%q want static-key", got)
	}
	if !c.Configured() {
		t.Fatal("Configured()=false with a usable static key; chat would answer 'not configured'")
	}
}

// TestConfiguredFollowsKeyFunc covers the state the console boots in on cluster
// once the static key is gone: no key yet, so chat degrades to the friendly
// error, and the moment the refresher resolves a token it is configured -- with
// no restart in between.
func TestConfiguredFollowsKeyFunc(t *testing.T) {
	token := ""
	c := New("http://gateway.test", "", "model")
	c.KeyFunc = func() string { return token }
	if c.Configured() {
		t.Fatal("Configured()=true with no credential at all")
	}
	token = "identity-token"
	if !c.Configured() {
		t.Fatal("Configured()=false after the identity token resolved; chat would stay dark until a restart")
	}
}

// TestRequestSendsResolvedKey proves the header on the wire, not just the
// accessor: a rotation reaching key() but not the Authorization header would
// still 401 at the gateway.
func TestRequestSendsResolvedKey(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	c := New(srv.URL, "static-key", "model")
	c.KeyFunc = func() string { return "identity-token" }
	if _, err := c.StreamChatCompletion(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "", nil); err != nil {
		t.Fatalf("StreamChatCompletion: %v", err)
	}
	if seen != "Bearer identity-token" {
		t.Fatalf("Authorization=%q want Bearer identity-token", seen)
	}
}
