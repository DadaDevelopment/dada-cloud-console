package tggateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// testA2AClient points the real client at an httptest server, so the assertion
// is on what actually goes on the wire rather than on a fake's bookkeeping.
func testA2AClient(srv *httptest.Server) *httpA2AClient {
	return &httpA2AClient{
		http:     srv.Client(),
		endpoint: func(string) string { return srv.URL },
	}
}

const a2aCompletedReply = `{"jsonrpc":"2.0","id":"tg-gateway","result":{"status":{"state":"completed"},"artifacts":[{"parts":[{"kind":"text","text":"ok"}]}]}}`

// TestSendWithContext_CarriesEndUserIdentity: a shared MCP tool server learns
// whose account a call is for only from a header the agent replays, and the
// integration broker refuses a call that arrives without one. Before this
// header existed the gateway sent Content-Type and nothing else, so every end
// user reached a tool server as nobody.
func TestSendWithContext_CarriesEndUserIdentity(t *testing.T) {
	var gotEndUser, gotAgent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEndUser = r.Header.Get(endUserHeader)
		gotAgent = r.Header.Get(agentHeader)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(a2aCompletedReply))
	}))
	defer srv.Close()

	reply, err := testA2AClient(srv).SendWithContext(context.Background(), "support-agent", a2aContextFor("1090977163"), "привет")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if reply != "ok" {
		t.Fatalf("reply = %q", reply)
	}
	if gotEndUser != "telegram:1090977163" {
		t.Errorf("end user header = %q, want telegram:1090977163", gotEndUser)
	}
	if gotAgent != "support-agent" {
		t.Errorf("agent header = %q, want support-agent", gotAgent)
	}
}

// TestSendWithContext_NoIdentityWhenContextIsNotAChat: a contextId in another
// shape carries no chat, so no identity is claimed. Guessing one would be worse
// than refusing -- the broker would serve some user's connected accounts to a
// caller nobody identified.
func TestSendWithContext_NoIdentityWhenContextIsNotAChat(t *testing.T) {
	var sawEndUser bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawEndUser = r.Header[http.CanonicalHeaderKey(endUserHeader)]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(a2aCompletedReply))
	}))
	defer srv.Close()

	if _, err := testA2AClient(srv).Send(context.Background(), "support-agent", "привет"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if sawEndUser {
		t.Errorf("an identity header must not be invented when the context carries no chat")
	}
}

// TestEndUserFromContextID covers the shapes the derivation must handle.
func TestEndUserFromContextID(t *testing.T) {
	cases := map[string]string{
		"tg-chat-123":  "telegram:123",
		"tg-chat-":     "",
		"runtime-abcd": "",
		"":             "",
	}
	for in, want := range cases {
		if got := endUserFromContextID(in); got != want {
			t.Errorf("endUserFromContextID(%q) = %q, want %q", in, got, want)
		}
	}
}
