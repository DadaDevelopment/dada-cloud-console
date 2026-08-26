package supporttask

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubTokenSource struct{ tok string }

func (s stubTokenSource) Token(context.Context) (string, error) { return s.tok, nil }

func TestNewReturnsNilWhenUnconfigured(t *testing.T) {
	if c := New("", stubTokenSource{tok: "t"}); c != nil {
		t.Fatalf("expected nil client for empty base URL, got %v", c)
	}
	if c := New("https://agent-sync-hub.dada-tuda.ru", nil); c != nil {
		t.Fatalf("expected nil client for nil token source, got %v", c)
	}
}

func TestIntake_PostsBearerAndBoundedBody(t *testing.T) {
	var gotAuth, gotPath string
	var body map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/support/intake", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "req-1", "created": true})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, stubTokenSource{tok: "tok"})
	res, err := c.Intake(context.Background(), Request{
		SupportTaskID: "fb-1", Title: "Payments fail", Report: "500 on checkout",
		Requester: "user@example.com", ProjectKey: "acme", AppName: "web",
	})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	if res.ID != "req-1" || !res.Created {
		t.Fatalf("res=%+v want {req-1 true}", res)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("auth=%q", gotAuth)
	}
	if gotPath != "/v1/support/intake" {
		t.Fatalf("path=%q", gotPath)
	}
	if body["support_task_id"] != "fb-1" || body["title"] != "Payments fail" || body["report"] != "500 on checkout" {
		t.Fatalf("body=%v", body)
	}
	if _, ok := body["prompt"]; ok {
		t.Fatal("body must never carry a prompt field")
	}
	if _, ok := body["callback_url"]; ok {
		t.Fatal("body must never carry a callback_url field")
	}
}

func TestIntake_RepeatCallReturnsCreatedFalse(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/support/intake", func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusCreated)
		created := calls == 1
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "req-1", "created": created})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, stubTokenSource{tok: "tok"})
	first, err := c.Intake(context.Background(), Request{SupportTaskID: "fb-1", Title: "t", Report: "r"})
	if err != nil {
		t.Fatalf("first intake: %v", err)
	}
	second, err := c.Intake(context.Background(), Request{SupportTaskID: "fb-1", Title: "t", Report: "r"})
	if err != nil {
		t.Fatalf("second intake: %v", err)
	}
	if !first.Created || second.Created {
		t.Fatalf("first=%+v second=%+v want first.Created=true second.Created=false", first, second)
	}
	if first.ID != second.ID {
		t.Fatalf("ids differ: %q vs %q", first.ID, second.ID)
	}
}

func TestIntake_ErrorStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/support/intake", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"cloud_task_forbidden"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, stubTokenSource{tok: "tok"})
	if _, err := c.Intake(context.Background(), Request{SupportTaskID: "fb-1", Title: "t", Report: "r"}); err == nil {
		t.Fatal("expected error on 403 status")
	}
}
