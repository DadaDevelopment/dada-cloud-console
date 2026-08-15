package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// The backend runs two replicas behind an ingress with no session affinity, so a
// client's initialize and its follow-up tools/list routinely land on different
// pods. When the transport keeps session state in a pod's memory, the second pod
// answers "session not found" (404) and the connector reports that it could not
// load tools. This exercises exactly that split: initialize against one handler,
// tools/list against a second, independently built one.
func TestToolsListSurvivesReplicaSwitch(t *testing.T) {
	raw, err := os.ReadFile("../api/docs/swagger.json")
	if err != nil {
		t.Fatalf("read swagger.json: %v", err)
	}
	cfg := Config{
		BackendURL:    "http://127.0.0.1:8080",
		ResourceURL:   "https://console.dada-tuda.ru/mcp",
		OverridesPath: "",
	}
	podA, err := NewHandler(raw, cfg)
	if err != nil {
		t.Fatalf("build pod A: %v", err)
	}
	podB, err := NewHandler(raw, cfg)
	if err != nil {
		t.Fatalf("build pod B: %v", err)
	}

	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"1"}}}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(initBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	podA.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("initialize on pod A: got %d, body %q", rec.Code, rec.Body.String())
	}
	sid := rec.Header().Get("Mcp-Session-Id")

	listBody := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	req2 := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(listBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Accept", "application/json, text/event-stream")
	if sid != "" {
		req2.Header.Set("Mcp-Session-Id", sid)
	}
	rec2 := httptest.NewRecorder()
	podB.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("tools/list on the other replica: got %d, body %q", rec2.Code, rec2.Body.String())
	}

	names := toolNamesFromStream(t, rec2.Body.String())
	if len(names) == 0 {
		t.Fatalf("tools/list returned no tools; body %q", rec2.Body.String())
	}
}

func toolNamesFromStream(t *testing.T, body string) []string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		payload, ok := strings.CutPrefix(strings.TrimSpace(line), "data: ")
		if !ok {
			if !strings.HasPrefix(strings.TrimSpace(line), "{") {
				continue
			}
			payload = strings.TrimSpace(line)
		}
		var env struct {
			Error  *json.RawMessage `json:"error"`
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			continue
		}
		if env.Error != nil {
			t.Fatalf("tools/list returned error: %s", string(*env.Error))
		}
		names := make([]string, 0, len(env.Result.Tools))
		for _, tl := range env.Result.Tools {
			names = append(names, tl.Name)
		}
		return names
	}
	return nil
}
