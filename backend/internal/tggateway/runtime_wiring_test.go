package tggateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeClientConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, url, token string
		enabled, invalid bool
	}{
		{name: "disabled"},
		{name: "token alone does not enable", token: "test-token"},
		{name: "missing token", url: "http://runtime", invalid: true},
		{name: "blank token", url: "http://runtime", token: " ", invalid: true},
		{name: "invalid URL", url: "runtime", token: "test-token", invalid: true},
		{name: "URL credentials rejected", url: "https://user:pass@runtime", token: "test-token", invalid: true},
		{name: "configured", url: "http://runtime/", token: "test-token", enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewRuntimeClientFromConfig(tc.url, tc.token)
			if (err != nil) != tc.invalid || (client != nil) != tc.enabled {
				t.Fatalf("client enabled=%v err=%v", client != nil, err)
			}
		})
	}
}

func TestAuthenticatedRuntimeClientContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/message" || r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("unexpected runtime method/path/authentication")
		}
		var req RuntimeMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ExternalID != "42" {
			t.Errorf("invalid request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(RuntimeMessageResponse{Suppressed: true, ReplyToChannelMessageID: "101"})
	}))
	defer server.Close()
	client, err := NewRuntimeClientFromConfig(server.URL+"/", "test-token")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.ProcessMessage(context.Background(), RuntimeMessageRequest{ExternalID: "42"})
	if err != nil || !resp.Suppressed || resp.ReplyToChannelMessageID != "101" {
		t.Fatalf("response=%+v err=%v", resp, err)
	}
}

type runtimeWiringTelegram struct {
	onceTelegram
	typing atomic.Int32
}

func (tg *runtimeWiringTelegram) SendChatAction(context.Context, string, int64, string) error {
	tg.typing.Add(1)
	return nil
}
func (tg *runtimeWiringTelegram) SendMessageWithLocationButton(ctx context.Context, token string, chatID int64, text string) error {
	return tg.SendMessage(ctx, token, chatID, text)
}

type runtimeWiringA2A struct {
	fakeA2A
	calls    atomic.Int32
	mu       sync.Mutex
	contexts []string
}

func (a *runtimeWiringA2A) SendWithContext(_ context.Context, _ string, contextID string, _ string) (string, error) {
	a.calls.Add(1)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.contexts = append(a.contexts, contextID)
	return "direct reply", nil
}

func TestRuntimeConfiguredRouting(t *testing.T) {
	for _, tc := range []struct {
		name             string
		status           int
		response         RuntimeMessageResponse
		expectedMessages int
	}{
		{"failure", 503, RuntimeMessageResponse{}, 0},
		{"suppressed", 200, RuntimeMessageResponse{Suppressed: true, Text: "must never send this"}, 0},
		{"empty", 200, RuntimeMessageResponse{Text: " \n "}, 0},
	} {
		for _, debounce := range []bool{false, true} {
			name := tc.name + "/immediate"
			var cfg *DebounceConfig
			if debounce {
				name = tc.name + "/debounced"
				cfg = &DebounceConfig{QuietWindow: 10 * time.Millisecond, MaxWindow: time.Second}
			}
			t.Run(name, func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.WriteHeader(tc.status)
					_ = json.NewEncoder(w).Encode(tc.response)
				}))
				defer server.Close()
				tg := &runtimeWiringTelegram{onceTelegram: onceTelegram{updates: []TelegramUpdate{{UpdateID: 1, ChatID: 42, Text: "hello"}}}}
				a2a := &runtimeWiringA2A{}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				go runPollerDebounced(ctx, tg, a2a, NewAuthenticatedRuntimeClient(server.URL, "test-token"), Binding{AgentName: "agent", BotToken: "token"}, cfg)
				waitFor(t, func() bool { return calls.Load() == 1 })
				if tc.expectedMessages > 0 {
					waitFor(t, func() bool { return tg.sentCount() == tc.expectedMessages })
				} else {
					time.Sleep(40 * time.Millisecond)
				}
				if tg.sentCount() != tc.expectedMessages || tg.typing.Load() != 0 || a2a.calls.Load() != 0 {
					t.Fatalf("messages=%d typing=%d a2a=%d", tg.sentCount(), tg.typing.Load(), a2a.calls.Load())
				}
			})
		}
	}
}

func TestRuntimeBatchPreservesMetadataAndReplyAnchor(t *testing.T) {
	for _, debounce := range []bool{false, true} {
		name := "immediate"
		var cfg *DebounceConfig
		if debounce {
			name = "debounced"
			cfg = &DebounceConfig{QuietWindow: 10 * time.Millisecond, MaxWindow: time.Second}
		}
		t.Run(name, func(t *testing.T) {
			requests := make(chan RuntimeMessageRequest, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req RuntimeMessageRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				requests <- req
				_ = json.NewEncoder(w).Encode(RuntimeMessageResponse{Text: "one batch reply", ReplyToChannelMessageID: "101"})
			}))
			defer server.Close()
			sentAt := time.Unix(1750000000, 0).UTC()
			tg := &runtimeWiringTelegram{onceTelegram: onceTelegram{updates: []TelegramUpdate{
				{UpdateID: 1, ChatID: 42, UserID: 43, Username: "fixture", FirstName: "Fixture", MessageID: 101, Text: "first", ThreadID: 9, ReplyToMessageID: 99, SentAt: sentAt},
				{UpdateID: 2, ChatID: 42, UserID: 43, MessageID: 102, Text: "second"},
			}}}
			a2a := &runtimeWiringA2A{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go runPollerDebounced(ctx, tg, a2a, NewAuthenticatedRuntimeClient(server.URL, "test-token"), Binding{AgentName: "agent", BotToken: "token"}, cfg)
			waitFor(t, func() bool { return tg.sentCount() == 1 })
			if len(requests) != 1 || a2a.calls.Load() != 0 {
				t.Fatal("batch did not use exactly one runtime call")
			}
			req := <-requests
			if req.AgentName != "agent" || req.ExternalID != "42" || req.Actor.ExternalID != "43" || req.Actor.Username != "fixture" || req.Actor.Metadata["first_name"] != "Fixture" || len(req.Messages) != 2 {
				t.Fatalf("lost batch identity: %+v", req)
			}
			first := req.Messages[0]
			if first.Content != "first" || first.ChannelMessageID != "101" || first.ThreadID != "9" || first.ReplyToChannelMessageID != "99" || first.SourceSentAt == nil || !first.SourceSentAt.Equal(sentAt) || req.Messages[1].ChannelMessageID != "102" {
				t.Fatalf("lost message identity: %+v", req.Messages)
			}
			tg.mu.Lock()
			defer tg.mu.Unlock()
			if len(tg.repliedTo) != 1 || tg.repliedTo[0] != 101 {
				t.Fatalf("reply anchors=%v", tg.repliedTo)
			}
		})
	}
}

func TestRuntimeImmediatePollSeparatesChats(t *testing.T) {
	tg := &onceTelegram{updates: []TelegramUpdate{{UpdateID: 1, ChatID: 42, UserID: 42, MessageID: 1, Text: "chat 42"}, {UpdateID: 2, ChatID: 99, UserID: 99, MessageID: 2, Text: "chat 99"}}}
	rt := &recordingRuntime{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runPoller(ctx, tg, fakeA2A{}, rt, Binding{AgentName: "agent"})
	waitFor(t, func() bool { return tg.sentCount() == 2 })
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.calls) != 2 {
		t.Fatalf("calls=%d", len(rt.calls))
	}
	seen := map[string]bool{}
	for _, req := range rt.calls {
		if len(req.Messages) != 1 || req.Actor.ExternalID != req.ExternalID || req.Messages[0].Content != "chat "+req.ExternalID {
			t.Fatalf("cross-chat contamination: %+v", req)
		}
		seen[req.ExternalID] = true
	}
	if !seen["42"] || !seen["99"] {
		t.Fatalf("chat routing=%v", seen)
	}
}

func TestRuntimeDisabledKeepsDirectStableContext(t *testing.T) {
	for _, tc := range []struct {
		name string
		rt   RuntimeClient
	}{{"nil", nil}, {"legacy noop", NewNoopRuntimeClient()}} {
		t.Run(tc.name, func(t *testing.T) {
			tg := &onceTelegram{updates: []TelegramUpdate{{UpdateID: 1, ChatID: 42, Text: "first"}, {UpdateID: 2, ChatID: 42, Text: "second"}}}
			a2a := &runtimeWiringA2A{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go runPoller(ctx, tg, a2a, tc.rt, Binding{AgentName: "agent"})
			waitFor(t, func() bool { return tg.sentCount() == 1 })
			a2a.mu.Lock()
			defer a2a.mu.Unlock()
			if len(a2a.contexts) != 2 || a2a.contexts[0] != "tg-chat-42" || a2a.contexts[1] != "tg-chat-42" {
				t.Fatalf("legacy contexts=%v", a2a.contexts)
			}
		})
	}
}

func TestRuntimeAgentScope(t *testing.T) {
	if _, err := ParseRuntimeAgents(" , ", true); err == nil {
		t.Fatal("unscoped rollout accepted")
	}
	names, err := ParseRuntimeAgents(" tg-exchange-support , ", true)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(nil, nil, nil, nil)
	client := NewAuthenticatedRuntimeClient("http://runtime", "test")
	m.SetRuntimeClient(client)
	m.SetRuntimeAgents(names)
	if m.runtimeForAgent("tg-exchange-support") != client || m.runtimeForAgent("another-agent") != nil {
		t.Fatal("runtime scope crossed binding boundary")
	}
}
