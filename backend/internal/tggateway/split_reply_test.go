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

// Plan 3.1a, revised: a client message landing between two parts hurries the
// rest of the series out without pacing. The runtime already holds the whole
// turn in the transcript, so dropping the tail would leave the model
// remembering words the client never saw.
func TestRunPollerDebounced_ClientMidSeriesFlushesTheTail(t *testing.T) {
	t.Setenv("TG_GATEWAY_SPLIT_REPLY", "1")
	tg := &timedTelegram{
		sequentialTelegram: sequentialTelegram{batches: [][]TelegramUpdate{
			{{UpdateID: 1, ChatID: 23, ChatType: "private", MessageID: 101, Text: "привет"}},
			{{UpdateID: 2, ChatID: 23, ChatType: "private", MessageID: 102, Text: "стой, другое"}},
		}},
		gaps:  []time.Duration{0, 1500 * time.Millisecond},
		scale: 1,
	}
	rt := &seriesRuntime{parts: []string{"раз", "два", "три"}, later: "ответ на второе"}
	cfg := DebounceConfig{QuietWindow: 20 * time.Millisecond, MaxWindow: time.Second, Pacing: splitPacing(2 * time.Second)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runPollerDebounced(ctx, tg, fakeA2A{}, rt, Binding{AgentName: "agent-tail", BotToken: "tok"}, &cfg)

	if !waitLong(t, 8*time.Second, func() bool { return tg.sentCount() == 4 }) {
		tg.mu.Lock()
		got := append([]string(nil), tg.sent...)
		tg.mu.Unlock()
		t.Fatalf("expected the whole series and the restarted answer, got %#v", got)
	}
	time.Sleep(time.Second)

	tg.mu.Lock()
	defer tg.mu.Unlock()
	want := []string{"раз", "два", "три", "ответ на второе"}
	if len(tg.sent) != len(want) {
		t.Fatalf("want the whole series then the new answer, got %#v", tg.sent)
	}
	for i := range want {
		if tg.sent[i] != want[i] {
			t.Fatalf("want %#v, got %#v", want, tg.sent)
		}
	}
	if tg.at[2].Sub(tg.at[1]) > 500*time.Millisecond {
		t.Fatalf("the hurried parts must go out without the %v gap, got %v", 2*time.Second, tg.at[2].Sub(tg.at[1]))
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

type holdRuntime struct {
	mu    sync.Mutex
	calls []RuntimeMessageRequest
}

func (r *holdRuntime) ProcessMessage(_ context.Context, req RuntimeMessageRequest) (RuntimeMessageResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, req)
	return RuntimeMessageResponse{Text: fmt.Sprintf("ответ %d", len(r.calls))}, nil
}

func (r *holdRuntime) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// workingDayZone names an IANA zone in which the clock reads 14:00 right
// now, so the tail hold is inside its working-day window whatever the host
// clock says.
func workingDayZone() string {
	offset := (14 - time.Now().UTC().Hour() + 24) % 24
	if offset > 12 {
		return fmt.Sprintf("Etc/GMT+%d", 24-offset)
	}
	return fmt.Sprintf("Etc/GMT-%d", offset)
}

// Plan 5, review #2: the hold is taken before the agent is called. A client
// message during the hold cancels the unclaimed run and carries its batch
// into the next one, so the runtime sees the whole thought once and never
// persists a reply the customer did not get.
func TestRunPollerDebounced_TailHoldIsTakenBeforeTheRuntimeCall(t *testing.T) {
	t.Setenv("TG_GATEWAY_PACING_TAIL_SHARE", "1")
	t.Setenv("TG_GATEWAY_PACING_TAIL_MIN_MS", "1500")
	t.Setenv("TG_GATEWAY_PACING_TAIL_MAX_MS", "1500")
	t.Setenv("TG_GATEWAY_TZ", workingDayZone())
	tg := &timedTelegram{
		sequentialTelegram: sequentialTelegram{batches: [][]TelegramUpdate{
			{{UpdateID: 1, ChatID: 24, ChatType: "private", MessageID: 101, Text: "привет"}},
			{{UpdateID: 2, ChatID: 24, ChatType: "private", MessageID: 102, Text: "вопрос"}},
			{{UpdateID: 3, ChatID: 24, ChatType: "private", MessageID: 103, Text: "ау, вы тут?"}},
		}},
		gaps:  []time.Duration{0, 300 * time.Millisecond, 500 * time.Millisecond},
		scale: 1,
	}
	rt := &holdRuntime{}
	cfg := DebounceConfig{QuietWindow: 20 * time.Millisecond, MaxWindow: time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runPollerDebounced(ctx, tg, fakeA2A{}, rt, Binding{AgentName: "agent-hold", BotToken: "tok"}, &cfg)

	if !waitLong(t, 8*time.Second, func() bool { return tg.sentCount() == 2 }) {
		tg.mu.Lock()
		got := append([]string(nil), tg.sent...)
		tg.mu.Unlock()
		t.Fatalf("expected the first answer and one answer to the held thought, got %#v", got)
	}
	time.Sleep(2 * time.Second)

	if rt.callCount() != 2 {
		t.Fatalf("the runtime must be called once per delivered answer, got %d calls", rt.callCount())
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.calls[0].DelaySeconds != 0 {
		t.Fatalf("the first message of a dialogue is never held, got delay_s=%d", rt.calls[0].DelaySeconds)
	}
	if len(rt.calls[1].Messages) != 2 || rt.calls[1].Messages[0].Content != "вопрос" || rt.calls[1].Messages[1].Content != "ау, вы тут?" {
		t.Fatalf("the message that landed during the hold must ride in the same batch, got %#v", rt.calls[1].Messages)
	}
	tg.mu.Lock()
	defer tg.mu.Unlock()
	if len(tg.sent) != 2 || tg.sent[0] != "ответ 1" || tg.sent[1] != "ответ 2" {
		t.Fatalf("want one answer per thought, got %#v", tg.sent)
	}
}
