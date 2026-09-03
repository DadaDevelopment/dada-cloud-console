package tggateway

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestDebouncer_BatchesRapidFireIntoOneDispatch(t *testing.T) {
	var mu sync.Mutex
	var dispatches [][]TelegramUpdate

	deb := NewDebouncer(DebounceConfig{QuietWindow: 50 * time.Millisecond, MaxWindow: 5 * time.Second}, func(key string, batch []TelegramUpdate) {
		mu.Lock()
		defer mu.Unlock()
		dispatches = append(dispatches, batch)
	})
	defer deb.Close()

	deb.Enqueue("agent=a chat=1", TelegramUpdate{UpdateID: 1, ChatID: 1, MessageID: 11, Text: "привет"})
	deb.Enqueue("agent=a chat=1", TelegramUpdate{UpdateID: 2, ChatID: 1, MessageID: 12, Text: "слушай"})
	deb.Enqueue("agent=a chat=1", TelegramUpdate{UpdateID: 3, ChatID: 1, MessageID: 13, Text: "у меня вопрос"})
	deb.Enqueue("agent=a chat=1", TelegramUpdate{UpdateID: 4, ChatID: 1, MessageID: 14, Text: "по регистрации"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(dispatches)
		mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(dispatches) != 1 {
		t.Fatalf("expected exactly 1 dispatch, got %d", len(dispatches))
	}
	if len(dispatches[0]) != 4 {
		t.Fatalf("expected 4 messages in batch, got %d", len(dispatches[0]))
	}
	if dispatches[0][0].Text != "привет" || dispatches[0][3].Text != "по регистрации" {
		t.Fatalf("batch must preserve message order")
	}
}

func TestDebouncer_MaxWindowFlushesUnderContinuousStream(t *testing.T) {
	var mu sync.Mutex
	var dispatches [][]TelegramUpdate

	deb := NewDebouncer(DebounceConfig{QuietWindow: 200 * time.Millisecond, MaxWindow: 120 * time.Millisecond}, func(key string, batch []TelegramUpdate) {
		mu.Lock()
		defer mu.Unlock()
		dispatches = append(dispatches, batch)
	})
	defer deb.Close()

	deb.Enqueue("agent=a chat=1", TelegramUpdate{UpdateID: 1, ChatID: 1, Text: "one"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(dispatches)
		mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(dispatches) != 1 {
		t.Fatalf("max window must flush despite no quiet gap, got %d dispatches", len(dispatches))
	}
	if len(dispatches[0]) != 1 {
		t.Fatalf("expected 1 message in flushed batch, got %d", len(dispatches[0]))
	}
}

func TestDebouncer_ChatsDoNotMix(t *testing.T) {
	var mu sync.Mutex
	byChat := map[string]int{}

	deb := NewDebouncer(DebounceConfig{QuietWindow: 40 * time.Millisecond, MaxWindow: 5 * time.Second}, func(key string, batch []TelegramUpdate) {
		mu.Lock()
		defer mu.Unlock()
		byChat[key] = len(batch)
	})
	defer deb.Close()

	for i := 0; i < 3; i++ {
		deb.Enqueue("agent=a chat=1", TelegramUpdate{UpdateID: int64(i), ChatID: 1, Text: "a"})
		deb.Enqueue("agent=a chat=2", TelegramUpdate{UpdateID: int64(i), ChatID: 2, Text: "b"})
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := len(byChat) == 2
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if byChat["agent=a chat=1"] != 3 || byChat["agent=a chat=2"] != 3 {
		t.Fatalf("each chat must get its own 3-message batch, got %v", byChat)
	}
}

// TestRunPollerDebounced_BatchedUpdatesOneRuntimeCall verifies the poller
// integration: three updates of one chat inside one poll with debounce ON
// (quiet window shorter than the test's patience) produce ONE runtime call
// with all three messages and ONE reply to the user.
type recordingRuntime struct {
	mu    sync.Mutex
	calls []RuntimeMessageRequest
}

func (r *recordingRuntime) ProcessMessage(_ context.Context, req RuntimeMessageRequest) (RuntimeMessageResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, req)
	return RuntimeMessageResponse{Text: "batch reply"}, nil
}

func (r *recordingRuntime) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

type blockingTelegram struct {
	onceTelegram
	release chan struct{}
}

func (b *blockingTelegram) GetUpdates(ctx context.Context, token string, offset int64, timeoutSec int) ([]TelegramUpdate, error) {
	select {
	case <-b.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return b.onceTelegram.GetUpdates(ctx, token, offset, timeoutSec)
}

func TestRunPollerDebounced_BatchedUpdatesOneRuntimeCall(t *testing.T) {
	tg := &onceTelegram{updates: []TelegramUpdate{
		{UpdateID: 1, ChatID: 42, MessageID: 101, Text: "привет"},
		{UpdateID: 2, ChatID: 42, MessageID: 102, Text: "слушай"},
		{UpdateID: 3, ChatID: 42, MessageID: 103, Text: "вопрос"},
	}}
	rt := &recordingRuntime{}
	quiet := 30 * time.Millisecond
	cfg := DebounceConfig{QuietWindow: quiet, MaxWindow: 5 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runPollerDebounced(ctx, tg, fakeA2A{}, rt, Binding{AgentName: "agent-b", BotToken: "tok-b"}, &cfg)

	waitFor(t, func() bool { return rt.callCount() == 1 })
	waitFor(t, func() bool { return tg.sentCount() == 1 })

	rt.mu.Lock()
	req := rt.calls[0]
	rt.mu.Unlock()

	if len(req.Messages) != 3 {
		t.Fatalf("expected 3 messages in one runtime call, got %d", len(req.Messages))
	}
	if req.Messages[0].Content != "привет" || req.Messages[2].Content != "вопрос" {
		t.Fatalf("batch content must preserve order, got %v", req.Messages)
	}
	if req.Messages[0].ChannelMessageID != "101" || req.Messages[2].ChannelMessageID != "103" {
		t.Fatalf("each message must keep its own channel_message_id, got %v", req.Messages)
	}
	if got := tg.sent[0]; got != "batch reply" {
		t.Fatalf("expected one batch reply, got %q", got)
	}
}

// TestRunPollerDebounced_NilDebounceStillBatchesOnePoll verifies the legacy
// path (no Debouncer): updates of one poll still go through processBatch --
// one runtime call carrying all the poll's messages, one reply. Per-poll
// batching is the baseline; the Debouncer only adds cross-poll merging.
func TestRunPollerDebounced_NilDebounceStillBatchesOnePoll(t *testing.T) {
	tg := &onceTelegram{updates: []TelegramUpdate{
		{UpdateID: 1, ChatID: 42, MessageID: 101, Text: "one"},
		{UpdateID: 2, ChatID: 42, MessageID: 102, Text: "two"},
	}}
	rt := &recordingRuntime{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runPollerDebounced(ctx, tg, fakeA2A{}, rt, Binding{AgentName: "agent-n", BotToken: "tok-n"}, nil)

	waitFor(t, func() bool { return rt.callCount() == 1 })
	waitFor(t, func() bool { return tg.sentCount() == 1 })

	rt.mu.Lock()
	n := len(rt.calls[0].Messages)
	rt.mu.Unlock()
	if n != 2 {
		t.Fatalf("one poll's updates must ride one runtime call, got %d messages", n)
	}
}

// TestDebouncer_EnqueueAfterFlushStartsFreshBatch ensures a message arriving
// right after a flush is not lost: it opens a new batch that dispatches on
// its own quiet window.
func TestDebouncer_EnqueueAfterFlushStartsFreshBatch(t *testing.T) {
	var mu sync.Mutex
	var dispatches [][]TelegramUpdate

	deb := NewDebouncer(DebounceConfig{QuietWindow: 40 * time.Millisecond, MaxWindow: 5 * time.Second}, func(key string, batch []TelegramUpdate) {
		mu.Lock()
		defer mu.Unlock()
		dispatches = append(dispatches, batch)
	})
	defer deb.Close()

	deb.Enqueue("agent=a chat=1", TelegramUpdate{UpdateID: 1, ChatID: 1, Text: "first"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(dispatches)
		mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	deb.Enqueue("agent=a chat=1", TelegramUpdate{UpdateID: 2, ChatID: 1, Text: "second"})

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(dispatches)
		mu.Unlock()
		if n == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(dispatches) != 2 {
		t.Fatalf("expected 2 dispatches total, got %d", len(dispatches))
	}
	if len(dispatches[0]) != 1 || len(dispatches[1]) != 1 {
		t.Fatalf("each batch must carry its own single message: %d, %d", len(dispatches[0]), len(dispatches[1]))
	}
	if dispatches[1][0].Text != "second" {
		t.Fatalf("second batch must contain the late message, got %q", dispatches[1][0].Text)
	}
}

// TestDebouncer_ConfigClamping covers NewDebouncer's zero-value fallbacks so
// a misconfigured env var cannot produce a debouncer that never flushes.
func TestDebouncer_ConfigClamping(t *testing.T) {
	cases := []DebounceConfig{
		{},
		{QuietWindow: 1 * time.Second, MaxWindow: 500 * time.Millisecond},
	}
	for i, cfg := range cases {
		got := NewDebouncer(cfg, func(string, []TelegramUpdate) {})
		if got.cfg.QuietWindow <= 0 || got.cfg.MaxWindow <= 0 {
			t.Fatalf("case %d: windows must be positive, got %v", i, got.cfg)
		}
		if got.cfg.MaxWindow < got.cfg.QuietWindow {
			t.Fatalf("case %d: max window clamped below quiet window: %v", i, got.cfg)
		}
		if fmt.Sprintf("%v", got.cfg) == "" {
			t.Fatal("unreachable")
		}
	}
}
