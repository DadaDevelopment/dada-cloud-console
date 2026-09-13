package tggateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// commentUpdate is a real-shaped getUpdates entry for a comment posted under
// a channel post in the linked discussion group.
const commentUpdate = `{
  "update_id": 42,
  "message": {
    "message_id": 1001,
    "date": 1788830000,
    "chat": {"id": -1001234, "type": "supergroup"},
    "from": {"id": 77, "username": "petya", "first_name": "Петя", "is_bot": false},
    "text": "а это точно быстрее?",
    "message_thread_id": 900,
    "reply_to_message": {
      "message_id": 900,
      "sender_chat": {"id": -1009999, "type": "channel"},
      "text": "Выкатили новый рантайм, холодный старт 5 секунд"
    }
  }
}`

func parseOne(t *testing.T, payload string) TelegramUpdate {
	t.Helper()
	var raw tgUpdate
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	upd, ok := updateFromRaw(raw)
	if !ok {
		t.Fatalf("expected the update to carry content")
	}
	return upd
}

func TestUpdateFromRaw_CarriesTheQuotedChannelPost(t *testing.T) {
	upd := parseOne(t, commentUpdate)
	if upd.ReplyToText != "Выкатили новый рантайм, холодный старт 5 секунд" {
		t.Fatalf("quoted post lost: %q", upd.ReplyToText)
	}
	if !upd.ReplyToIsChannel {
		t.Fatalf("expected the quoted message to be recognised as the channel post")
	}
	if upd.ReplyToMessageID != 900 || upd.ThreadID != 900 {
		t.Fatalf("thread anchors: reply=%d thread=%d", upd.ReplyToMessageID, upd.ThreadID)
	}
	if got := QuotedContext(upd); got == "" {
		t.Fatalf("expected the comment to travel with its post")
	}
}

func TestUpdateFromRaw_CaptionStandsInForText(t *testing.T) {
	payload := `{"update_id":43,"message":{"message_id":2,"date":1,"chat":{"id":-1,"type":"supergroup"},
	"from":{"id":7,"username":"p"},"text":"это что за график?",
	"reply_to_message":{"message_id":1,"sender_chat":{"id":-2,"type":"channel"},"caption":"p99 после перехода на пул"}}}`
	upd := parseOne(t, payload)
	if upd.ReplyToText != "p99 после перехода на пул" {
		t.Fatalf("caption not used as quoted text: %q", upd.ReplyToText)
	}
}

func TestUpdateFromRaw_EmptyMessageIsDropped(t *testing.T) {
	var raw tgUpdate
	if err := json.Unmarshal([]byte(`{"update_id":44,"message":{"message_id":3,"date":1,"chat":{"id":-1,"type":"supergroup"},"from":{"id":7}}}`), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := updateFromRaw(raw); ok {
		t.Fatalf("a message with no text, media or location must not reach the agent")
	}
}

func TestRedactToken_StripsTokenFromMessage(t *testing.T) {
	msg := `Post "https://api.telegram.org/bot123456:SECRETVALUE/getUpdates": dial tcp: connection refused`
	got := redactToken(msg, "123456:SECRETVALUE")
	if strings.Contains(got, "SECRETVALUE") {
		t.Fatalf("token survived redaction: %q", got)
	}
	if !strings.Contains(got, "REDACTED") {
		t.Fatalf("expected redaction marker, got %q", got)
	}
}

func TestGetUpdates_TransportFailureDoesNotLeakToken(t *testing.T) {
	srv := httptest.NewServer(nil)
	srv.Close()

	client := NewTelegramClient(srv.URL)
	_, err := client.GetUpdates(context.Background(), "123456:SECRETVALUE", 0, 1)
	if err == nil {
		t.Fatalf("expected a transport error against a closed server")
	}
	if strings.Contains(err.Error(), "SECRETVALUE") {
		t.Fatalf("getUpdates error leaked bot token: %v", err)
	}
}
