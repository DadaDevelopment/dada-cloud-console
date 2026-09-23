package tggateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSendWithContext_FailedTaskNeverLeaksArtifactText covers the live
// incident: a kagent A2A task can end with status.state == "failed" and
// still carry the raw upstream error - here a leaked z.ai 429 rate-limit
// body ("Error code: 429 - ... Weekly/Monthly Limit Exhausted") - inside
// artifacts, in the exact shape a completed reply uses. SendWithContext must
// recognize the task did not complete from status.state alone and never let
// extractText read that artifact as if it were the agent's answer.
func TestSendWithContext_FailedTaskNeverLeaksArtifactText(t *testing.T) {
	leaked := `Error code: 429 - {"error": {"code": "1310", "message": "Weekly/Monthly Limit Exhausted. Your limit will reset at 2026-09-23 22:42:41"}}`
	leakedJSON, err := json.Marshal(leaked)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	body := `{"jsonrpc":"2.0","id":"tg-gateway","result":{"status":{"state":"failed"},"artifacts":[{"parts":[{"kind":"text","text":` +
		string(leakedJSON) + `}]}]}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	reply, sendErr := testA2AClient(srv).Send(context.Background(), "tg-vibecoder", "а бенчмарки найдешь?)")
	if sendErr != nil {
		t.Fatalf("send: %v", sendErr)
	}
	if reply != a2aFailureFallback {
		t.Fatalf("reply = %q, want the generic fallback %q - a failed task's artifact text must never reach the chat", reply, a2aFailureFallback)
	}
}
