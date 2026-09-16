package tggateway

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// seriesRuntime answers the first turn with a cut-up reply and every later
// turn with a single message, so a test can tell a series apart from what the
// restarted run says.
type seriesRuntime struct {
	mu    sync.Mutex
	parts []string
	later string
	calls int
}

func (r *seriesRuntime) ProcessMessage(_ context.Context, _ RuntimeMessageRequest) (RuntimeMessageResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.calls == 1 {
		return RuntimeMessageResponse{Text: strings.Join(r.parts, " "), Messages: r.parts}, nil
	}
	return RuntimeMessageResponse{Text: r.later}, nil
}

func splitPacing(gap time.Duration) *PacingConfig {
	return &PacingConfig{
		BaseQuiet: 20 * time.Millisecond, MaxQuiet: 50 * time.Millisecond,
		CharsPerMinute: 250, MinTyping: time.Millisecond, MaxTyping: 2 * time.Millisecond,
		GapMin: gap, GapMax: gap,
	}
}

func TestGapFor_StaysInsideItsBounds(t *testing.T) {
	p := &PacingConfig{GapMin: 10 * time.Second, GapMax: 40 * time.Second}
	seen := map[time.Duration]bool{}
	for i := 0; i < 200; i++ {
		g := p.GapFor()
		if g < p.GapMin || g >= p.GapMax {
			t.Fatalf("gap %v outside [%v, %v)", g, p.GapMin, p.GapMax)
		}
		seen[g] = true
	}
	if len(seen) < 10 {
		t.Fatalf("gap must vary, got %d distinct values", len(seen))
	}
	fixed := &PacingConfig{GapMin: 5 * time.Second, GapMax: 5 * time.Second}
	if got := fixed.GapFor(); got != 5*time.Second {
		t.Fatalf("a degenerate range must return the bound itself, got %v", got)
	}
	empty := &PacingConfig{}
	if got := empty.GapFor(); got != pacingGapMinDefault {
		t.Fatalf("unset bounds must fall back to the default, got %v", got)
	}
}

func TestSendableParts_DropsSilenceAndBlanks(t *testing.T) {
	got := sendableParts([]string{"раз", "   ", "SKIP", "два"})
	if len(got) != 2 || got[0] != "раз" || got[1] != "два" {
		t.Fatalf("want the two real parts, got %#v", got)
	}
}

// With TG_GATEWAY_SPLIT_REPLY unset the gateway must ignore Messages
// entirely: today's behaviour is one send per turn.
func TestRunPollerDebounced_SplitOffSendsOneMessage(t *testing.T) {
	tg := &sequentialTelegram{batches: [][]TelegramUpdate{
		{{UpdateID: 1, ChatID: 21, ChatType: "private", MessageID: 101, Text: "привет"}},
	}}
	rt := &seriesRuntime{parts: []string{"раз", "два", "три"}, later: "потом"}
	cfg := DebounceConfig{QuietWindow: 20 * time.Millisecond, MaxWindow: time.Second, Pacing: splitPacing(10 * time.Millisecond)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runPollerDebounced(ctx, tg, fakeA2A{}, rt, Binding{AgentName: "agent-nosplit", BotToken: "tok"}, &cfg)

	waitFor(t, func() bool { return tg.sentCount() == 1 })
	time.Sleep(150 * time.Millisecond)

	tg.mu.Lock()
	defer tg.mu.Unlock()
	if len(tg.sent) != 1 || tg.sent[0] != "раз два три" {
		t.Fatalf("split off must send the glued turn once, got %#v", tg.sent)
	}
}

func TestRunPollerDebounced_SplitOnSendsThePartsInOrder(t *testing.T) {
	t.Setenv("TG_GATEWAY_SPLIT_REPLY", "1")
	tg := &sequentialTelegram{batches: [][]TelegramUpdate{
		{{UpdateID: 1, ChatID: 22, ChatType: "private", MessageID: 101, Text: "привет"}},
	}}
	rt := &seriesRuntime{parts: []string{"раз", "два", "три"}, later: "потом"}
	cfg := DebounceConfig{QuietWindow: 20 * time.Millisecond, MaxWindow: time.Second, Pacing: splitPacing(10 * time.Millisecond)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runPollerDebounced(ctx, tg, fakeA2A{}, rt, Binding{AgentName: "agent-split", BotToken: "tok"}, &cfg)

	waitFor(t, func() bool { return tg.sentCount() == 3 })

	tg.mu.Lock()
	defer tg.mu.Unlock()
	if tg.sent[0] != "раз" || tg.sent[1] != "два" || tg.sent[2] != "три" {
		t.Fatalf("parts must arrive in order, got %#v", tg.sent)
	}
	if len(tg.repliedTo) != 0 {
		t.Fatalf("a private series must not quote the client, got %v", tg.repliedTo)
	}
}

// Plan 3.1a: a client message landing between two parts drops the unsent
// tail, and what was already sent stays sent.
func TestRunPollerDebounced_ClientMidSeriesDropsTheTail(t *testing.T) {
	t.Setenv("TG_GATEWAY_SPLIT_REPLY", "1")
	tg := &timedTelegram{
		sequentialTelegram: sequentialTelegram{batches: [][]TelegramUpdate{
			{{UpdateID: 1, ChatID: 23, ChatType: "private", MessageID: 101, Text: "привет"}},
			{{UpdateID: 2, ChatID: 23, ChatType: "private", MessageID: 102, Text: "стой, другое"}},
		}},
		// The second message has to land while the series is in flight. The
		// pacing quiet window has a one-second floor (QuietFor), so the first
		// part goes out a little after a second and the 2s gap to the next
		// part is the window this message lands in.
		gaps:  []time.Duration{0, 1500 * time.Millisecond},
		scale: 1,
	}
	rt := &seriesRuntime{parts: []string{"раз", "два", "три"}, later: "ответ на второе"}
	cfg := DebounceConfig{QuietWindow: 20 * time.Millisecond, MaxWindow: time.Second, Pacing: splitPacing(2 * time.Second)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runPollerDebounced(ctx, tg, fakeA2A{}, rt, Binding{AgentName: "agent-tail", BotToken: "tok"}, &cfg)

	if !waitLong(t, 8*time.Second, func() bool { return tg.sentCount() == 2 }) {
		tg.mu.Lock()
		got := append([]string(nil), tg.sent...)
		tg.mu.Unlock()
		t.Fatalf("expected the first part and the restarted answer, got %#v", got)
	}
	time.Sleep(3 * time.Second)

	tg.mu.Lock()
	defer tg.mu.Unlock()
	if len(tg.sent) != 2 {
		t.Fatalf("want the first part plus the new answer, got %#v", tg.sent)
	}
	if tg.sent[0] != "раз" {
		t.Fatalf("what was sent stays sent, got %#v", tg.sent)
	}
	if tg.sent[1] != "ответ на второе" {
		t.Fatalf("the restarted run must answer the new message, got %#v", tg.sent)
	}
	for _, text := range tg.sent {
		if text == "два" || text == "три" {
			t.Fatalf("the tail must not reach the chat, got %#v", tg.sent)
		}
	}
}

// Review M1: a superseded run's deferred markTail(false) must not clear the
// tail of the run that replaced it, or the new series becomes uncancellable.
func TestMarkTail_IgnoresASupersededGeneration(t *testing.T) {
	s := newInterruptState()
	ctx := context.Background()

	oldCtx, oldDone, _, _ := s.begin("c1", ctx, nil)
	if !s.claimReply("c1", oldCtx) {
		t.Fatal("first run must win its claim")
	}
	s.markTail("c1", oldCtx, true)
	oldDone()

	newCtx, newDone, _, _ := s.begin("c1", ctx, nil)
	defer newDone()
	if !s.claimReply("c1", newCtx) {
		t.Fatal("second run must win its claim")
	}
	s.markTail("c1", newCtx, true)

	// The old run unwinding now: its deferred call names a generation that no
	// longer exists and must change nothing.
	s.markTail("c1", oldCtx, false)

	if stale := s.cancelUnclaimed("c1"); stale != nil {
		t.Fatalf("a claimed run carries no batch back, got %v", stale)
	}
	if newCtx.Err() == nil {
		t.Fatal("the new run's tail must still be cancellable")
	}
}

// Review M3: the first-message tracker is bounded, and forgetting a chat errs
// towards "first" (no tail delay).
func TestSeenChats_ExpiresAndStaysBounded(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	s := newSeenChats(time.Hour, 4)

	if !s.firstTime("a", now) {
		t.Fatal("an unknown chat is first")
	}
	if s.firstTime("a", now.Add(time.Minute)) {
		t.Fatal("a known chat is not first")
	}
	if !s.firstTime("a", now.Add(2*time.Hour)) {
		t.Fatal("a chat older than the TTL counts as first again")
	}

	for i := 0; i < 50; i++ {
		s.firstTime(fmt.Sprintf("chat-%d", i), now.Add(time.Duration(i)*time.Second))
	}
	if len(s.seen) > 4 {
		t.Fatalf("tracker grew to %d entries, want at most 4", len(s.seen))
	}
}
