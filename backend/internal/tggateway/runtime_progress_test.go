package tggateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeProgressDrivesTypingBeforeReply(t *testing.T) {
	ready := make(chan struct{})
	release := make(chan struct{})
	var once atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/x-ndjson" {
			t.Error("missing stream negotiation")
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"event":"processing"}`)
		w.(http.Flusher).Flush()
		if once.CompareAndSwap(false, true) {
			close(ready)
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprintln(w, `{"event":"result","result":{"text":"Ответ"}}`)
	}))
	defer server.Close()
	tg := &runtimeWiringTelegram{onceTelegram: onceTelegram{updates: []TelegramUpdate{{UpdateID: 1, ChatID: 42, Text: "question"}}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runPollerDebounced(ctx, tg, &runtimeWiringA2A{}, NewAuthenticatedRuntimeClient(server.URL, "test-token"), Binding{AgentName: "agent", BotToken: "token"}, nil)
	<-ready
	waitFor(t, func() bool { return tg.typing.Load() > 0 })
	if tg.sentCount() != 0 {
		t.Fatal("reply before model completion")
	}
	close(release)
	waitFor(t, func() bool { return tg.sentCount() == 1 })
	cancel()
}
func TestRuntimeProgressSuppressionAndIncompleteStream(t *testing.T) {
	for _, tc := range []struct {
		name, wire         string
		suppressed, failed bool
	}{
		{"paused", `{"event":"result","result":{"suppressed":true}}`, true, false},
		{"lost after admission", `{"event":"processing"}`, false, true},
		{"failure", `{"event":"error"}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/x-ndjson")
				fmt.Fprintln(w, tc.wire)
			}))
			defer server.Close()
			client := NewAuthenticatedRuntimeClient(server.URL, "test-token").(*httpRuntimeClient)
			calls := 0
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			out, err := client.ProcessMessageWithProgress(ctx, RuntimeMessageRequest{}, func() { calls++ })
			if (err != nil) != tc.failed || out.Suppressed != tc.suppressed {
				t.Fatalf("result=%+v err=%v", out, err)
			}
			if tc.suppressed && calls != 0 {
				t.Fatal("paused turn typed")
			}
		})
	}
}
