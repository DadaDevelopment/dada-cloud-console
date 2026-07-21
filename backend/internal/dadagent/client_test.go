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
		IntentID: "int-1", Summary: "do thing", TaskType: "feature",
		CoreLoopImpact: "instrument site", PrimaryPillar: "SPD",
		VisiblePrimitives: []string{"intents"}, KPIHypothesis: []string{"orchestration_success_rate"},
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
	if gotPath != "/v1/agentsync/intents" || body["task_type"] != "feature" {
		t.Fatalf("path=%q body=%v", gotPath, body)
	}
}

func TestAutofix_PostsBearerAndBody(t *testing.T) {
	var gotAuth, gotPath string
	var body map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/runs/autofix", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"run_id": "run_abc123", "status": "queued"})
	})
	agent := httptest.NewServer(mux)
	defer agent.Close()

	kc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 300})
	}))
	defer kc.Close()

	c := New(agent.URL, NewTokenSource(kc.URL, "cid", "sec"))
	res, err := c.Autofix(context.Background(), AutofixRequest{
		RepoFullName: "acme/widgets", InstallToken: "ghs_installtoken",
		Error: "panic: nil pointer in handler", CallbackURL: "https://console.dada-tuda.ru/api/v1/webhooks/dadagent",
	})
	if err != nil {
		t.Fatalf("autofix: %v", err)
	}
	if res.RunID != "run_abc123" || res.Status != "queued" {
		t.Fatalf("res=%+v want run_abc123/queued", res)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("auth=%q", gotAuth)
	}
	if gotPath != "/v1/runs/autofix" || body["repo_full_name"] != "acme/widgets" || body["error"] != "panic: nil pointer in handler" {
		t.Fatalf("path=%q body=%v", gotPath, body)
	}
}

func TestAutofix_ErrorStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/runs/autofix", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":"invalid payload"}`))
	})
	agent := httptest.NewServer(mux)
	defer agent.Close()
	kc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 300})
	}))
	defer kc.Close()

	c := New(agent.URL, NewTokenSource(kc.URL, "cid", "sec"))
	if _, err := c.Autofix(context.Background(), AutofixRequest{RepoFullName: "acme/widgets", InstallToken: "t", Error: "boom"}); err == nil {
		t.Fatal("expected error on 422 status")
	}
}

func TestGetRun_ParsesCloudTaskID(t *testing.T) {
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/runs/run_abc123", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"run_id": "run_abc123", "cloud_task_id": "run-deadbeef01", "status": "running",
			"source": "autofix", "repo": "acme/widgets", "agent": "codex",
			"created_at": "2026-01-01T00:00:00Z",
		})
	})
	agent := httptest.NewServer(mux)
	defer agent.Close()
	kc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 300})
	}))
	defer kc.Close()

	c := New(agent.URL, NewTokenSource(kc.URL, "cid", "sec"))
	info, err := c.GetRun(context.Background(), "run_abc123")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if info.CloudTaskID != "run-deadbeef01" {
		t.Fatalf("cloud_task_id=%q want run-deadbeef01", info.CloudTaskID)
	}
	if gotPath != "/v1/runs/run_abc123" {
		t.Fatalf("path=%q", gotPath)
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
