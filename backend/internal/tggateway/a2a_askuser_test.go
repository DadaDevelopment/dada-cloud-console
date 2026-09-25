package tggateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

const pausedOnAskUser = `{"jsonrpc":"2.0","id":"x","result":{"kind":"task","id":"task-7","status":{"state":"input-required","message":{"role":"agent","parts":[{"kind":"data","data":{"name":"adk_request_confirmation","args":{"originalFunctionCall":{"name":"ask_user","args":{"questions":[{"question":"какая валюта?"},{"question":"сумма?"}]}}}}}]}}}}`

func TestA2AResumesAskUserPauseInsteadOfDeadEnd(t *testing.T) {
	var mu sync.Mutex
	var seen []a2aMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req a2aRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		seen = append(seen, req.Params.Message)
		n := len(seen)
		mu.Unlock()
		if n == 1 {
			_, _ = w.Write([]byte(pausedOnAskUser))
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"x","result":{"kind":"task","id":"task-7","status":{"state":"completed"},"artifacts":[{"parts":[{"kind":"text","text":"В какой валюте перевод?"}]}]}}`))
	}))
	defer srv.Close()
	c := testA2AClient(srv)

	reply, err := c.SendWithContext(context.Background(), "tg-vibecoder", "tg-chat-42", "хочу перевести")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "В какой валюте перевод?" {
		t.Fatalf("reply = %q, want the model's plain-text question", reply)
	}
	if len(seen) != 2 {
		t.Fatalf("calls = %d, want send + resume", len(seen))
	}
	resume := seen[1]
	if resume.TaskID != "task-7" || resume.ContextID != "tg-chat-42" || len(resume.Parts) != 1 || resume.Parts[0].Kind != "data" {
		t.Fatalf("resume = %+v", resume)
	}
	data := resume.Parts[0].Data
	answers, _ := data["ask_user_answers"].([]any)
	if data["decision_type"] != "approve" || len(answers) != 2 {
		t.Fatalf("resume data = %v, want approve with one answer per question", data)
	}
}

func TestA2AStillPausedAfterResumeFallsBack(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(pausedOnAskUser))
	}))
	defer srv.Close()
	c := testA2AClient(srv)
	reply, err := c.SendWithContext(context.Background(), "tg-vibecoder", "tg-chat-42", "hi")
	if err != nil || reply != a2aInputRequiredFallback || calls != 2 {
		t.Fatalf("reply=%q err=%v calls=%d", reply, err, calls)
	}
}
