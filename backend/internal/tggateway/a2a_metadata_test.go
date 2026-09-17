package tggateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSendWithContext_CarriesTelegramMetadata: the runtime names the trace's
// user and session after the Telegram sender only from message metadata; the
// identity line in the prompt text is invisible to it.
func TestSendWithContext_CarriesTelegramMetadata(t *testing.T) {
	var got a2aMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req a2aRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		got = req.Params.Message
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(a2aCompletedReply))
	}))
	defer srv.Close()

	u := TelegramUpdate{ChatID: -1001, UserID: 42, Username: "ivan_petrov", FirstName: "Иван", ThreadID: 7}
	ctx := WithA2AMetadata(context.Background(), TelegramA2AMetadata(u))
	if _, err := testA2AClient(srv).SendWithContext(ctx, "support-agent", a2aContextFor("-1001:7"), "привет"); err != nil {
		t.Fatalf("send: %v", err)
	}
	want := map[string]any{
		"dada.channel": "telegram", "dada.chat_id": "-1001", "dada.user_id": "42",
		"dada.username": "ivan_petrov", "dada.first_name": "Иван", "dada.thread_id": "7",
	}
	for k, v := range want {
		if got.Metadata[k] != v {
			t.Errorf("metadata[%s] = %v, want %v", k, got.Metadata[k], v)
		}
	}
}

func TestSendWithContext_NoMetadataWithoutIdentity(t *testing.T) {
	var got a2aMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req a2aRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		got = req.Params.Message
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(a2aCompletedReply))
	}))
	defer srv.Close()
	if _, err := testA2AClient(srv).SendWithContext(context.Background(), "support-agent", "", "привет"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got.Metadata != nil {
		t.Fatalf("metadata = %v, want none", got.Metadata)
	}
}
