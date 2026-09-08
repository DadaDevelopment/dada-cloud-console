package tggateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestObserver_ShipsSkippedCommentsToo(t *testing.T) {
	got := make(chan ObservedUpdate, 4)
	auth := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload ObservedUpdate
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode: %v", err)
		}
		auth <- r.Header.Get("Authorization")
		got <- payload
	}))
	defer srv.Close()

	t.Setenv("TG_OBSERVE_URL_TG_VIBECODER", srv.URL)
	t.Setenv("TG_OBSERVE_TOKEN_TG_VIBECODER", "s3cret")
	o := NewObserverForAgent("tg-vibecoder")
	if o == nil {
		t.Fatal("observer must be configured from the per-agent key")
	}

	u := groupMsg("ага")
	u.Username = "igor"
	o.Observe(context.Background(), u, EngageDecision{false, ReasonTooShort})

	select {
	case payload := <-got:
		if payload.Engaged || payload.Reason != ReasonTooShort {
			t.Fatalf("the skipped decision must travel with the message, got %+v", payload)
		}
		if payload.Text != "ага" || payload.Username != "igor" {
			t.Fatalf("payload lost the message, got %+v", payload)
		}
		if payload.Conversation != ConversationKey(u) {
			t.Fatalf("conversation = %q, want %q", payload.Conversation, ConversationKey(u))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("observation never arrived")
	}
	if header := <-auth; header != "Bearer s3cret" {
		t.Fatalf("authorization = %q, want the bearer token", header)
	}
}

func TestObserver_OffWithoutAUrlAndSafeWhenNil(t *testing.T) {
	t.Setenv("TG_OBSERVE_URL", "")
	t.Setenv("TG_OBSERVE_URL_TG_VIBECODER", "")
	if o := NewObserverForAgent("tg-vibecoder"); o != nil {
		t.Fatalf("no url must mean no sink, got %+v", o)
	}
	var nilObserver *Observer
	nilObserver.Observe(context.Background(), groupMsg("что-то"), EngageDecision{true, ReasonSubstantive})
}

func TestObserver_IgnoresPrivateChats(t *testing.T) {
	hits := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- struct{}{}
	}))
	defer srv.Close()
	t.Setenv("TG_OBSERVE_URL", srv.URL)
	o := NewObserverForAgent("tg-vibecoder")
	o.Observe(context.Background(), TelegramUpdate{ChatID: 1, ChatType: "private", Text: "личное"}, EngageDecision{true, ReasonPrivate})
	select {
	case <-hits:
		t.Fatal("a private chat must never be shipped to the observation sink")
	case <-time.After(300 * time.Millisecond):
	}
}
