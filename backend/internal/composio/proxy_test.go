package composio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool connects to the database the suite is pointed at, or skips.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping composio store tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// fakeComposio stands in for the hosted API: the session endpoint, the MCP
// endpoint, and nothing else the broker touches.
func fakeComposio(t *testing.T, mcpHits *int, lastKey *string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/tool_router/session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"session_id":"trs_fake","mcp":{"url":%q}}`, base+"/tool_router/trs_fake/mcp")
	})
	mux.HandleFunc("/tool_router/trs_fake/mcp", func(w http.ResponseWriter, r *http.Request) {
		*mcpHits++
		*lastKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"tools\":[]}}\n\n"))
	})
	srv := httptest.NewServer(mux)
	base = srv.URL
	t.Cleanup(srv.Close)
	return srv
}

func newTestProxy(t *testing.T, upstream string) (*Proxy, *Service, uuid.UUID) {
	t.Helper()
	pool := testPool(t)
	store := NewStore(pool)
	client := NewClient(upstream, "ak_test_key")
	svc := NewService(client, store, "dadatest")
	projectID := uuid.New()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM composio_tool_calls WHERE project_id = $1`, projectID)
		_, _ = pool.Exec(ctx, `DELETE FROM composio_integrations WHERE project_id = $1`, projectID)
		_, _ = pool.Exec(ctx, `DELETE FROM composio_sessions WHERE project_id = $1`, projectID)
	})
	return NewProxy(svc), svc, projectID
}

func rpcError(t *testing.T, body string) (int, string) {
	t.Helper()
	var parsed struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return parsed.Error.Code, parsed.Error.Message
}

// TestServeMCP_NoEndUserHeaderIsRefused: the agent runtime replays caller
// headers onto MCP calls, and that header is the only thing that says whose
// account this call is for. A call without it must not be served at all -- any
// session the broker picked would belong to some other user.
func TestServeMCP_NoEndUserHeaderIsRefused(t *testing.T) {
	hits := 0
	key := ""
	upstream := fakeComposio(t, &hits, &key)
	proxy, _, projectID := newTestProxy(t, upstream.URL)

	req := httptest.NewRequest(http.MethodPost, "/mcp/"+projectID.String(),
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	w := httptest.NewRecorder()
	proxy.ServeMCP(w, req, projectID)

	code, msg := rpcError(t, w.Body.String())
	if code != -32001 {
		t.Fatalf("expected the identity refusal, got code %d: %s", code, w.Body.String())
	}
	if !strings.Contains(msg, EndUserHeader) {
		t.Errorf("the refusal must name the missing header, got %q", msg)
	}
	if hits != 0 {
		t.Errorf("an unidentified call reached Composio %d times; it must never leave the broker", hits)
	}
}

// TestServeMCP_UnknownEndUserIsRefused: a user who never connected anything has
// no session, and inventing one would silently create an empty Composio session
// per stranger.
func TestServeMCP_UnknownEndUserIsRefused(t *testing.T) {
	hits := 0
	key := ""
	upstream := fakeComposio(t, &hits, &key)
	proxy, _, projectID := newTestProxy(t, upstream.URL)

	req := httptest.NewRequest(http.MethodPost, "/mcp/"+projectID.String(),
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set(EndUserHeader, "tg:nobody")
	w := httptest.NewRecorder()
	proxy.ServeMCP(w, req, projectID)

	code, _ := rpcError(t, w.Body.String())
	if code != -32002 {
		t.Fatalf("expected the not-connected refusal, got %s", w.Body.String())
	}
	if hits != 0 {
		t.Errorf("a call for an unknown user reached Composio %d times", hits)
	}
}

// TestServeMCP_RoutesToTheCallersOwnSessionAndAudits is the whole point of the
// broker: two end users of one project, one static agent-facing URL, each call
// landing in the caller's own session -- plus an audit row, which is the only
// record a tool call over MCP leaves anywhere in this platform.
func TestServeMCP_RoutesToTheCallersOwnSessionAndAudits(t *testing.T) {
	hits := 0
	key := ""
	upstream := fakeComposio(t, &hits, &key)
	proxy, svc, projectID := newTestProxy(t, upstream.URL)
	ctx := context.Background()
	mcpURL := upstream.URL + "/tool_router/trs_fake/mcp"

	alice := SessionRow{ProjectID: projectID, EndUserKey: "tg:alice", UserID: "u-alice",
		SessionID: "trs_alice", MCPURL: mcpURL, Toolkits: []string{"gmail"}}
	bob := SessionRow{ProjectID: projectID, EndUserKey: "tg:bob", UserID: "u-bob",
		SessionID: "trs_bob", MCPURL: mcpURL, Toolkits: []string{"github"}}
	for _, row := range []SessionRow{alice, bob} {
		if err := svc.store.SaveSession(ctx, row); err != nil {
			t.Fatalf("save session: %v", err)
		}
	}

	got, err := svc.store.Session(ctx, projectID, "tg:bob")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.SessionID != "trs_bob" {
		t.Fatalf("bob resolved to %q, one user must never resolve to another's session", got.SessionID)
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp/"+projectID.String(),
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"GMAIL_FETCH_EMAILS"}}`))
	req.Header.Set(EndUserHeader, "tg:alice")
	req.Header.Set(AgentHeader, "support-agent")
	req.Header.Set("Accept", "application/json, text/event-stream")
	w := httptest.NewRecorder()
	proxy.ServeMCP(w, req, projectID)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body.String())
	}
	if hits != 1 {
		t.Fatalf("expected exactly one upstream call, got %d", hits)
	}
	if key != "ak_test_key" {
		t.Errorf("the platform key must be added by the broker, upstream saw %q", key)
	}
	if !strings.Contains(w.Body.String(), "event: message") {
		t.Errorf("the SSE body must be relayed verbatim, got %q", w.Body.String())
	}

	var tool, agent string
	err = svc.store.pool.QueryRow(ctx,
		`SELECT tool_name, agent_name FROM composio_tool_calls
		  WHERE project_id = $1 AND end_user_key = $2 ORDER BY id DESC LIMIT 1`,
		projectID, "tg:alice").Scan(&tool, &agent)
	if err != nil {
		t.Fatalf("the audit row is the only trace an MCP tool call leaves: %v", err)
	}
	if tool != "GMAIL_FETCH_EMAILS" || agent != "support-agent" {
		t.Errorf("audit row = %q by %q, want GMAIL_FETCH_EMAILS by support-agent", tool, agent)
	}
}

// TestActiveToolkits_OnlyActiveCounts: a connected account exists from the
// moment a Connect Link is minted. Treating that as permission would put tools
// in front of an agent that answer 401, which reads to a user as a broken agent
// rather than an unfinished authorization.
func TestActiveToolkits_OnlyActiveCounts(t *testing.T) {
	hits := 0
	key := ""
	upstream := fakeComposio(t, &hits, &key)
	_, svc, projectID := newTestProxy(t, upstream.URL)
	ctx := context.Background()

	if err := svc.store.UpsertIntegration(ctx, projectID, "tg:alice", "gmail", "ca_1", "ACTIVE"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := svc.store.UpsertIntegration(ctx, projectID, "tg:alice", "github", "ca_2", "INITIALIZING"); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	active, err := svc.ActiveToolkits(ctx, projectID, "tg:alice")
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if len(active) != 1 || active[0] != "gmail" {
		t.Fatalf("active = %v, want only gmail", active)
	}
}

// TestComposioUserID_IsDerivedFromStableIDs: Composio keys every connected
// account on this string, so a value that can change (an email, a display name)
// would orphan a user's authorizations.
func TestComposioUserID_IsDerivedFromStableIDs(t *testing.T) {
	svc := NewService(nil, nil, "dada")
	id := uuid.MustParse("7a387969-e082-415c-8b61-1f53f7e18295")
	got := svc.ComposioUserID(id, "tg:1090977163")
	want := "dada:7a387969-e082-415c-8b61-1f53f7e18295:tg:1090977163"
	if got != want {
		t.Fatalf("user id = %q, want %q", got, want)
	}
}
