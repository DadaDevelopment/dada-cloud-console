package reflect

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dada-tuda/console/mcp-server/internal/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func callReq(args map[string]any) *mcp.CallToolRequest {
	raw, _ := json.Marshal(args)
	return &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: raw},
	}
}

func resultText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if len(r.Content) == 0 {
		t.Fatal("no content")
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] not text: %T", r.Content[0])
	}
	return tc.Text
}

func TestProxy_GetWithPathAndQuery(t *testing.T) {
	var gotMethod, gotPath, gotQuery, gotAuth string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"operations":[]}`))
	}))
	defer backend.Close()

	g := GeneratedTool{
		Name: "listOperations", Method: "GET",
		PathTemplate: "/projects/{projectId}/operations",
		PathParams:   []string{"projectId"},
		QueryParams:  []string{"limit"},
	}
	h := MakeHandler(g, backend.URL, "/api/v1")

	ctx := auth.WithBearer(context.Background(), "Bearer tok123")
	res, err := h(ctx, callReq(map[string]any{"projectId": "p1", "limit": 5}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, res))
	}
	if gotMethod != "GET" {
		t.Errorf("method = %s", gotMethod)
	}
	if gotPath != "/api/v1/projects/p1/operations" {
		t.Errorf("path = %s", gotPath)
	}
	if gotQuery != "limit=5" {
		t.Errorf("query = %s", gotQuery)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("auth = %s", gotAuth)
	}
}

func TestProxy_PostBody(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"operationId":"op-9","status":"Created"}`))
	}))
	defer backend.Close()

	g := GeneratedTool{
		Name: "createDatabase", Method: "POST",
		PathTemplate: "/projects/{projectId}/environments/{envId}/databases",
		PathParams:   []string{"projectId", "envId"},
		BodyProps:    []string{"appName", "plan"},
	}
	h := MakeHandler(g, backend.URL, "/api/v1")

	ctx := auth.WithBearer(context.Background(), "Bearer abc")
	res, err := h(ctx, callReq(map[string]any{
		"projectId": "p1", "envId": "e1", "appName": "web", "plan": "small",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, res))
	}
	if gotPath != "/api/v1/projects/p1/environments/e1/databases" {
		t.Errorf("path = %s", gotPath)
	}
	if gotAuth != "Bearer abc" {
		t.Errorf("auth = %s", gotAuth)
	}
	// path params must NOT leak into body.
	if _, has := gotBody["projectId"]; has {
		t.Errorf("projectId leaked into body: %v", gotBody)
	}
	if gotBody["appName"] != "web" || gotBody["plan"] != "small" {
		t.Errorf("body = %v", gotBody)
	}

	// 202 -> poll note prepended + body present.
	text := resultText(t, res)
	if !strings.Contains(text, "getOperation tool") {
		t.Errorf("202 result missing poll note: %s", text)
	}
	if !strings.Contains(text, "op-9") {
		t.Errorf("202 result missing body: %s", text)
	}
}

func TestProxy_4xxPassthrough(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"plan is required"}`))
	}))
	defer backend.Close()

	g := GeneratedTool{Name: "createDatabase", Method: "POST", PathTemplate: "/x"}
	h := MakeHandler(g, backend.URL, "/api/v1")

	res, err := h(context.Background(), callReq(map[string]any{"foo": "bar"}))
	if err != nil {
		t.Fatalf("4xx must not be a Go error: %v", err)
	}
	if !res.IsError {
		t.Error("4xx should set IsError")
	}
	if got := resultText(t, res); !strings.Contains(got, "plan is required") {
		t.Errorf("backend message not passed through: %s", got)
	}
}

func TestProxy_NetworkError(t *testing.T) {
	g := GeneratedTool{Name: "x", Method: "GET", PathTemplate: "/x"}
	// Unreachable backend.
	h := MakeHandler(g, "http://127.0.0.1:1", "/api/v1")

	res, err := h(context.Background(), callReq(nil))
	if err != nil {
		t.Fatalf("network error must be a tool result, not Go error: %v", err)
	}
	if !res.IsError {
		t.Error("network error should set IsError")
	}
	if got := resultText(t, res); !strings.Contains(got, "retry") {
		t.Errorf("network error missing retry hint: %s", got)
	}
}

func TestProxy_NoBearerOmitsHeader(t *testing.T) {
	var hadAuth bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer backend.Close()

	g := GeneratedTool{Name: "x", Method: "GET", PathTemplate: "/x"}
	h := MakeHandler(g, backend.URL, "/api/v1")
	if _, err := h(context.Background(), callReq(nil)); err != nil {
		t.Fatal(err)
	}
	if hadAuth {
		t.Error("Authorization header should be absent when no bearer in ctx")
	}
}
