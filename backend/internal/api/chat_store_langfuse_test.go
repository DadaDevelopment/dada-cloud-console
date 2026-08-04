package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/agentchat"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/google/uuid"
)

type langfuseTestServer struct {
	*httptest.Server
	mu            sync.Mutex
	traceRequests []string
	ingested      []map[string]any
}

func (lf *langfuseTestServer) queries() []string {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	return append([]string(nil), lf.traceRequests...)
}

func (lf *langfuseTestServer) bodies() []map[string]any {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	return append([]map[string]any(nil), lf.ingested...)
}

func newLangfuseTestServer(t *testing.T, traces []map[string]any) *langfuseTestServer {
	t.Helper()
	lf := &langfuseTestServer{}
	lf.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "pk" || pass != "sk" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch r.URL.Path {
		case "/api/public/ingestion":
			var body struct {
				Batch []struct {
					Body map[string]any `json:"body"`
				} `json:"batch"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			lf.mu.Lock()
			for _, ev := range body.Batch {
				lf.ingested = append(lf.ingested, ev.Body)
			}
			lf.mu.Unlock()
			w.Write([]byte(`{"successes":[],"errors":[]}`))
		case "/api/public/traces":
			lf.mu.Lock()
			lf.traceRequests = append(lf.traceRequests, r.URL.RawQuery)
			lf.mu.Unlock()
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			if page < 1 {
				page = 1
			}
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			if limit < 1 {
				limit = 50
			}
			start := (page - 1) * limit
			end := start + limit
			if start > len(traces) {
				start = len(traces)
			}
			if end > len(traces) {
				end = len(traces)
			}
			totalPages := (len(traces) + limit - 1) / limit
			if totalPages == 0 {
				totalPages = 1
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data": traces[start:end],
				"meta": map[string]any{
					"page":       page,
					"limit":      limit,
					"totalItems": len(traces),
					"totalPages": totalPages,
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(lf.Close)
	return lf
}

func langfuseTraceFixture(role, content string, meta map[string]any) map[string]any {
	tr := map[string]any{
		"id":     uuid.NewString(),
		"name":   agentChatMessageTracePrefix + role,
		"output": content,
	}
	if meta != nil {
		tr["metadata"] = meta
	}
	return tr
}

func newLangfuseStoreHandler(host string) *Handler {
	h := &Handler{cfg: &config.Config{
		AgentChatStore:    "langfuse",
		LangfuseHost:      host,
		LangfusePublicKey: "pk",
		LangfuseSecretKey: "sk",
		LangfuseEnabled:   true,
	}}
	h.chat = newChatStore(h)
	return h
}

func TestLangfuseChatStoreSessionMessagesReturnsOldestFirst(t *testing.T) {
	lf := newLangfuseTestServer(t, []map[string]any{
		langfuseTraceFixture("assistant", "third", nil),
		langfuseTraceFixture("tool", "second", map[string]any{"tool_name": "list_apps"}),
		langfuseTraceFixture("user", "first", nil),
	})
	h := newLangfuseStoreHandler(lf.URL)

	got, err := h.transcript().SessionMessages(context.Background(), uuid.New(), 0)
	if err != nil {
		t.Fatalf("SessionMessages: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 messages, got %d (%+v)", len(got), got)
	}
	if got[0].Content != "first" || got[2].Content != "third" {
		t.Fatalf("messages are not oldest-first: %+v", got)
	}
	if got[1].ToolName != "list_apps" {
		t.Fatalf("tool name lost: %+v", got[1])
	}
}

func TestLangfuseChatStoreSessionMessagesKeepsNewestUnderLimit(t *testing.T) {
	lf := newLangfuseTestServer(t, []map[string]any{
		langfuseTraceFixture("assistant", "newest", nil),
		langfuseTraceFixture("user", "middle", nil),
		langfuseTraceFixture("user", "oldest", nil),
	})
	h := newLangfuseStoreHandler(lf.URL)

	got, err := h.transcript().SessionMessages(context.Background(), uuid.New(), 2)
	if err != nil {
		t.Fatalf("SessionMessages: %v", err)
	}
	if len(got) != 2 || got[0].Content != "middle" || got[1].Content != "newest" {
		t.Fatalf("want the newest two oldest-first, got %+v", got)
	}
}

func TestLangfuseChatStoreSessionMessagesIgnoresForeignTraces(t *testing.T) {
	lf := newLangfuseTestServer(t, []map[string]any{
		{"id": uuid.NewString(), "name": "agent-chat-turn", "output": "not a message"},
		langfuseTraceFixture("system", "injected", nil),
		langfuseTraceFixture("user", "real", nil),
	})
	h := newLangfuseStoreHandler(lf.URL)

	got, err := h.transcript().SessionMessages(context.Background(), uuid.New(), 0)
	if err != nil {
		t.Fatalf("SessionMessages: %v", err)
	}
	if len(got) != 1 || got[0].Content != "real" {
		t.Fatalf("turn traces and unknown roles must not become chat messages, got %+v", got)
	}
}

func TestLangfuseChatStoreSessionMessagesPaginates(t *testing.T) {
	var traces []map[string]any
	for i := 0; i < 150; i++ {
		traces = append(traces, langfuseTraceFixture("user", fmt.Sprintf("m%d", 149-i), nil))
	}
	lf := newLangfuseTestServer(t, traces)
	h := newLangfuseStoreHandler(lf.URL)

	got, err := h.transcript().SessionMessages(context.Background(), uuid.New(), 0)
	if err != nil {
		t.Fatalf("SessionMessages: %v", err)
	}
	if len(got) != 150 {
		t.Fatalf("want all 150 messages across pages, got %d", len(got))
	}
	if got[0].Content != "m0" || got[149].Content != "m149" {
		t.Fatalf("pagination reordered the conversation: first=%q last=%q", got[0].Content, got[149].Content)
	}
	if len(lf.queries()) != 2 {
		t.Fatalf("want 2 page requests, got %d: %v", len(lf.queries()), lf.queries())
	}
}

func TestLangfuseChatStoreSessionMessagesReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	h := newLangfuseStoreHandler(srv.URL)

	if _, err := h.transcript().SessionMessages(context.Background(), uuid.New(), 0); err == nil {
		t.Fatal("a store that answers 500 must report an error, not an empty conversation")
	}
}

func TestLangfuseChatStoreDailyCountAsksForUserMessagesOnly(t *testing.T) {
	lf := newLangfuseTestServer(t, []map[string]any{
		langfuseTraceFixture("user", "a", nil),
		langfuseTraceFixture("user", "b", nil),
	})
	h := newLangfuseStoreHandler(lf.URL)

	count, err := h.transcript().DailyUserMessageCount(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("DailyUserMessageCount: %v", err)
	}
	if count != 2 {
		t.Fatalf("want 2, got %d", count)
	}
	if len(lf.queries()) != 1 {
		t.Fatalf("the cap must cost exactly one request, got %d", len(lf.queries()))
	}
	query := lf.queries()[0]
	for _, want := range []string{"userId=user-1", "name=chat-message-user", "limit=1", "fields=core", "fromTimestamp="} {
		if !strings.Contains(query, want) {
			t.Fatalf("query %q is missing %q", query, want)
		}
	}
}

func TestLangfuseChatStoreAppendMessageCarriesSessionAndTool(t *testing.T) {
	lf := newLangfuseTestServer(t, nil)
	h := newLangfuseStoreHandler(lf.URL)

	sessionID := uuid.New()
	ctx := agentchat.WithSessionID(context.Background(), sessionID)
	toolName := "list_apps"
	projectID := uuid.New()
	h.transcript().AppendMessage(ctx, "user-1", "dada", &projectID, nil, "tool", "result", &toolName)

	deadline := time.Now().Add(2 * time.Second)
	for len(lf.bodies()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	ingested := lf.bodies()
	if len(ingested) != 1 {
		t.Fatalf("want 1 ingested trace, got %d", len(ingested))
	}
	body := ingested[0]
	if body["name"] != agentChatMessageTracePrefix+"tool" {
		t.Fatalf("wrong trace name: %v", body["name"])
	}
	if body["sessionId"] != sessionID.String() {
		t.Fatalf("session id lost: %v", body["sessionId"])
	}
	if body["userId"] != "user-1" || body["output"] != "result" {
		t.Fatalf("unexpected body: %+v", body)
	}
	meta, _ := body["metadata"].(map[string]any)
	if meta["tool_name"] != "list_apps" || meta["project_id"] != projectID.String() {
		t.Fatalf("metadata lost: %+v", meta)
	}
}

func TestLangfuseChatStoreAppendMessageWithoutSessionIsDropped(t *testing.T) {
	lf := newLangfuseTestServer(t, nil)
	h := newLangfuseStoreHandler(lf.URL)

	h.transcript().AppendMessage(context.Background(), "user-1", "dada", nil, nil, "user", "hi", nil)

	time.Sleep(100 * time.Millisecond)
	if got := lf.bodies(); len(got) != 0 {
		t.Fatalf("a message with no session id has no conversation to belong to, got %+v", got)
	}
}

func TestNewChatStoreSelection(t *testing.T) {
	lf := newLangfuseTestServer(t, nil)
	cases := []struct {
		name     string
		cfg      config.Config
		wantKind any
	}{
		{"default", config.Config{}, pgChatStore{}},
		{"postgres", config.Config{AgentChatStore: "postgres"}, pgChatStore{}},
		{"unknown falls back", config.Config{AgentChatStore: "clickhouse"}, pgChatStore{}},
		{"langfuse without keys falls back", config.Config{AgentChatStore: "langfuse"}, pgChatStore{}},
		{"langfuse", config.Config{
			AgentChatStore:    "LangFuse",
			LangfuseHost:      lf.URL,
			LangfusePublicKey: "pk",
			LangfuseSecretKey: "sk",
			LangfuseEnabled:   true,
		}, langfuseChatStore{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			h := &Handler{cfg: &cfg}
			got := newChatStore(h)
			if fmt.Sprintf("%T", got) != fmt.Sprintf("%T", tc.wantKind) {
				t.Fatalf("want %T, got %T", tc.wantKind, got)
			}
		})
	}
}

func TestHandlerTranscriptDefaultsToPostgres(t *testing.T) {
	h := &Handler{cfg: &config.Config{}}
	if _, ok := h.transcript().(pgChatStore); !ok {
		t.Fatalf("a hand-built Handler must keep the postgres transcript, got %T", h.transcript())
	}
}
