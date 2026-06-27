package dadagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubmitIntent_PostsBearerAndBody(t *testing.T) {
	var gotAuth, gotPath string
	var body map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agentsync/intents", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted": true, "execution_mode": "auto",
			"workflow": map[string]any{"workflow_id": "wf-1"},
		})
	})
	agent := httptest.NewServer(mux)
	defer agent.Close()

	kc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 300})
	}))
	defer kc.Close()

	c := New(agent.URL, NewTokenSource(kc.URL, "cid", "sec"))
	res, err := c.SubmitIntent(context.Background(), IntentRequest{
		IntentID: "int-1", Summary: "do thing", TaskType: "yandex-metrika-goals",
		CoreLoopImpact: "instrument site", PrimaryPillar: "growth",
		VisiblePrimitives: []string{"web"}, KPIHypothesis: []KPI{{Name: "conv", Direction: "up"}},
		CloudPayload: map[string]any{"cloud_task_id": "ct-1"},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if res.WorkflowID != "wf-1" {
		t.Fatalf("workflow_id=%q want wf-1", res.WorkflowID)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("auth=%q", gotAuth)
	}
	if gotPath != "/v1/agentsync/intents" || body["task_type"] != "yandex-metrika-goals" {
		t.Fatalf("path=%q body=%v", gotPath, body)
	}
}

func TestExecuteIntent_PostsExecute(t *testing.T) {
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agentsync/intents/int-1/execute", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	agent := httptest.NewServer(mux)
	defer agent.Close()
	kc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 300})
	}))
	defer kc.Close()

	c := New(agent.URL, NewTokenSource(kc.URL, "cid", "sec"))
	if err := c.ExecuteIntent(context.Background(), "int-1"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/v1/agentsync/intents/int-1/execute" {
		t.Fatalf("path=%q", gotPath)
	}
}
