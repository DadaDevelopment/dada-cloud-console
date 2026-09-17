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
		case "/api/public/otel/v1/traces":
			if r.Header.Get("x-langfuse-ingestion-version") != "4" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var body struct {
				ResourceSpans []struct {
					ScopeSpans []struct {
						Spans []struct {
							ParentSpanID string `json:"parentSpanId"`
							Attributes   []struct {
								Key   string         `json:"key"`
								Value map[string]any `json:"value"`
							} `json:"attributes"`
						} `json:"spans"`
					} `json:"scopeSpans"`
				} `json:"resourceSpans"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			lf.mu.Lock()
			for _, rs := range body.ResourceSpans {
				for _, ss := range rs.ScopeSpans {
					for _, span := range ss.Spans {
						if span.ParentSpanID != "" {
							continue
						}
						flat := map[string]any{"metadata": map[string]any{}}
						for _, a := range span.Attributes {
							str, _ := a.Value["stringValue"].(string)
							switch {
							case a.Key == "langfuse.trace.name":
								flat["name"] = str
							case a.Key == "langfuse.user.id":
								flat["userId"] = str
							case a.Key == "langfuse.session.id":
								flat["sessionId"] = str
							case a.Key == "langfuse.trace.output":
								var out any
								if err := json.Unmarshal([]byte(str), &out); err != nil {
									out = str
								}
								flat["output"] = out
							case strings.HasPrefix(a.Key, "langfuse.trace.metadata."):
								flat["metadata"].(map[string]any)[strings.TrimPrefix(a.Key, "langfuse.trace.metadata.")] = str
							}
						}
						lf.ingested = append(lf.ingested, flat)
					}
				}
			}
			lf.mu.Unlock()
			w.Write([]byte(`{"partialSuccess":{}}`))
		case "/api/public/v2/observations":
			lf.mu.Lock()
			lf.traceRequests = append(lf.traceRequests, r.URL.RawQuery)
			lf.mu.Unlock()
			q := r.URL.Query()
			if q.Get("fromStartTime") == "" || q.Get("toStartTime") == "" {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"message":"fromStartTime and toStartTime are required"}`))
				return
			}
			start, _ := strconv.Atoi(q.Get("cursor"))
			limit, _ := strconv.Atoi(q.Get("limit"))
			if limit < 1 || limit > 1000 {
				limit = 50
			}
			end := start + limit
			if start > len(traces) {
				start = len(traces)
			}
			if end > len(traces) {
				end = len(traces)
			}
			cursor := ""
			if end < len(traces) {
				cursor = strconv.Itoa(end)
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data": traces[start:end],
				"meta": map[string]any{"cursor": cursor},
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
		"id":                uuid.NewString(),
		"traceId":           uuid.NewString(),
		"traceName":         agentChatMessageTracePrefix + role,
		"name":              agentChatMessageTracePrefix + role,
		"output":            content,
		"isRootObservation": true,
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
		{"id": uuid.NewString(), "traceId": uuid.NewString(), "traceName": "agent-chat-turn", "name": "agent-chat-turn", "output": "not a message", "isRootObservation": true},
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
	for i := 0; i < 600; i++ {
		traces = append(traces, langfuseTraceFixture("user", fmt.Sprintf("m%d", 599-i), nil))
	}
	lf := newLangfuseTestServer(t, traces)
	h := newLangfuseStoreHandler(lf.URL)

	got, err := h.transcript().SessionMessages(context.Background(), uuid.New(), 0)
	if err != nil {
		t.Fatalf("SessionMessages: %v", err)
	}
	if len(got) != agentChatStoreMaxMessages {
		t.Fatalf("want the newest %d messages across pages, got %d", agentChatStoreMaxMessages, len(got))
	}
	if got[0].Content != "m100" || got[499].Content != "m599" {
		t.Fatalf("pagination reordered the conversation: first=%q last=%q", got[0].Content, got[499].Content)
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
	for _, want := range []string{"userId=user-1", "name=chat-message-user", "isRootObservation=true", "limit=1000", "fields=core&", "fromStartTime=", "toStartTime="} {
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
