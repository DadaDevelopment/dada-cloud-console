package tggateway

import (
	"context"
	"sync"
	"testing"
	"time"
)

// ownerBurst is the owner's timeline verbatim: three messages 2 s and 4 s
// apart that must read as ONE thought and earn ONE reply.
var ownerBurst = []struct {
	text string
	gap  time.Duration
}{
	{"привет", 0},
	{"хочу", 2 * time.Second},
	{"с вами работать", 4 * time.Second},
}

// timedTelegram serves one poll per entry, holding each poll back by its
// gap so the messages arrive in separate getUpdates rounds exactly as they
// would from a person typing.
type timedTelegram struct {
	sequentialTelegram
	gaps  []time.Duration
	scale float64
}

func (s *timedTelegram) GetUpdates(ctx context.Context, token string, offset int64, timeoutSec int) ([]TelegramUpdate, error) {
	s.mu.Lock()
	i := s.i
	s.mu.Unlock()
	if i < len(s.gaps) {
		select {
		case <-time.After(time.Duration(float64(s.gaps[i]) * s.scale)):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.sequentialTelegram.GetUpdates(ctx, token, offset, timeoutSec)
}

func waitLong(t *testing.T, patience time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(patience)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// TestPacing_OwnerBurstNeverSplitsAtProductionNumbers is the arithmetic
// guarantee with the live configuration: with TG_GATEWAY_PACING=1 and the
// live 20 s base, the SHORTEST quiet window any message of the burst can
// draw (worst-case jitter floor) is still longer than the gap to the next
// message, so the Debouncer cannot flush between them.
func TestPacing_OwnerBurstNeverSplitsAtProductionNumbers(t *testing.T) {
	t.Setenv("TG_GATEWAY_PACING", "1")
	t.Setenv("TG_GATEWAY_PACING_BASE_MS", "20000")
	t.Setenv("TG_GATEWAY_PACING_MAX_QUIET_MS", "")
	p := PacingFromEnv()
	if p == nil {
		t.Fatal("pacing must be on")
	}
	p.JitterSigma = 0
	for i := 0; i+1 < len(ownerBurst); i++ {
		u := TelegramUpdate{Text: ownerBurst[i].text}
		floor := time.Duration(float64(p.QuietFor([]TelegramUpdate{u})) * pacingJitterFloor)
		next := ownerBurst[i+1].gap
		if floor <= next {
			t.Fatalf("%q: shortest quiet %v must exceed the %v gap to %q", u.Text, floor, next, ownerBurst[i+1].text)
		}
	}
}

// TestRunPollerDebounced_OwnerBurstAcrossPollsOneReply runs the owner's
// timeline through the real poller with pacing on (scaled 1:10, jitter
// off): three polls, one runtime call carrying all three messages, one
// reply sent.
func TestRunPollerDebounced_OwnerBurstAcrossPollsOneReply(t *testing.T) {
	const scale = 0.1
	batches := make([][]TelegramUpdate, 0, len(ownerBurst))
	gaps := make([]time.Duration, 0, len(ownerBurst))
	for i, m := range ownerBurst {
		batches = append(batches, []TelegramUpdate{{UpdateID: int64(i + 1), ChatID: 42, MessageID: int64(100 + i), Text: m.text}})
		gaps = append(gaps, m.gap)
	}
	tg := &timedTelegram{sequentialTelegram: sequentialTelegram{batches: batches}, gaps: gaps, scale: scale}
	rt := &recordingRuntime{}
	pacing := &PacingConfig{
		BaseQuiet:      time.Duration(20 * float64(time.Second) * scale),
		MaxQuiet:       time.Duration(90 * float64(time.Second) * scale),
		CharsPerMinute: 250,
		MinTyping:      10 * time.Millisecond,
		MaxTyping:      20 * time.Millisecond,
	}
	cfg := DebounceConfig{QuietWindow: 2500 * time.Millisecond, MaxWindow: 8 * time.Second, Pacing: pacing}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runPollerDebounced(ctx, tg, fakeA2A{}, rt, Binding{AgentName: "agent-burst", BotToken: "tok"}, &cfg)

	if !waitLong(t, 15*time.Second, func() bool { return tg.sentCount() >= 1 }) {
		t.Fatal("no reply within patience")
	}
	time.Sleep(time.Duration(3 * float64(time.Second) * scale))

	rt.mu.Lock()
	calls := append([]RuntimeMessageRequest(nil), rt.calls...)
	rt.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("burst must be ONE runtime call, got %d", len(calls))
	}
	if len(calls[0].Messages) != 3 {
		t.Fatalf("the one call must carry all 3 messages, got %d", len(calls[0].Messages))
	}
	for i, m := range ownerBurst {
		if calls[0].Messages[i].Content != m.text {
			t.Fatalf("message %d: want %q, got %q", i, m.text, calls[0].Messages[i].Content)
		}
	}
	if n := tg.sentCount(); n != 1 {
		t.Fatalf("burst must earn ONE reply, got %d: %v", n, tg.sent)
	}
}

// gatedTelegram serves batch i only after gate i is closed, so a test can
// deliver the next message at a chosen moment of the previous run.
type gatedTelegram struct {
	sequentialTelegram
	gates []chan struct{}
}

func (g *gatedTelegram) GetUpdates(ctx context.Context, token string, offset int64, timeoutSec int) ([]TelegramUpdate, error) {
	g.mu.Lock()
	i := g.i
	g.mu.Unlock()
	if i < len(g.gates) && g.gates[i] != nil {
		select {
		case <-g.gates[i]:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return g.sequentialTelegram.GetUpdates(ctx, token, offset, timeoutSec)
}

// restartRuntime blocks its first call until release (or cancel) and
// answers every later call at once, recording each request it saw.
type restartRuntime struct {
	mu      sync.Mutex
	release chan struct{}
	calls   []RuntimeMessageRequest
}

func (r *restartRuntime) ProcessMessage(ctx context.Context, req RuntimeMessageRequest) (RuntimeMessageResponse, error) {
	r.mu.Lock()
	r.calls = append(r.calls, req)
	n := len(r.calls)
	r.mu.Unlock()
	if n == 1 {
		select {
		case <-r.release:
			return RuntimeMessageResponse{Text: "slow reply"}, nil
		case <-ctx.Done():
			return RuntimeMessageResponse{}, ctx.Err()
		}
	}
	return RuntimeMessageResponse{Text: "fast reply"}, nil
}

func (r *restartRuntime) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// TestRunPollerDebounced_MessageDuringGenerationRestartsWithFullBatch: the
// gap between two messages outran the quiet window, so message 1 is
// already at the agent when message 2 lands. A person composing a reply
// would stop and re-read; the poller must cancel the unfinished run,
// carry message 1 into the new batch, and answer both messages ONCE.
func TestRunPollerDebounced_MessageDuringGenerationRestartsWithFullBatch(t *testing.T) {
	rt := &restartRuntime{release: make(chan struct{})}
	second := make(chan struct{})
	tg := &gatedTelegram{sequentialTelegram: sequentialTelegram{batches: [][]TelegramUpdate{
		{{UpdateID: 1, ChatID: 7, MessageID: 1, Text: "привет"}},
		{{UpdateID: 2, ChatID: 7, MessageID: 2, Text: "хочу с вами работать"}},
	}}, gates: []chan struct{}{nil, second}}
	cfg := DebounceConfig{QuietWindow: 30 * time.Millisecond, MaxWindow: 5 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runPollerDebounced(ctx, tg, fakeA2A{}, rt, Binding{AgentName: "agent-gen", BotToken: "tok"}, &cfg)

	if !waitLong(t, 3*time.Second, func() bool { return rt.callCount() == 1 }) {
		t.Fatal("run 1 never reached the agent")
	}
	close(second)

	if !waitLong(t, 3*time.Second, func() bool { return tg.sentCount() >= 1 }) {
		t.Fatalf("no reply after the second message; runtime calls=%d", rt.callCount())
	}
	time.Sleep(100 * time.Millisecond)
	close(rt.release)
	time.Sleep(100 * time.Millisecond)

	tg.mu.Lock()
	sent := append([]string(nil), tg.sent...)
	tg.mu.Unlock()
	if len(sent) != 1 || sent[0] != "fast reply" {
		t.Fatalf("exactly one reply, from the restarted run, got %v", sent)
	}
	rt.mu.Lock()
	calls := append([]RuntimeMessageRequest(nil), rt.calls...)
	rt.mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("expected the canceled run plus one restart, got %d calls", len(calls))
	}
	last := calls[1]
	if len(last.Messages) != 2 || last.Messages[0].Content != "привет" || last.Messages[1].Content != "хочу с вами работать" {
		t.Fatalf("the restarted run must carry message 1 ahead of message 2, got %+v", last.Messages)
	}
}

// TestRunPollerDebounced_MessageDuringTypingKeepsComposedReply pins the
// other side of the line: once the agent has answered and the bot is only
// typing the reply out, a new message does not unsend it. The composed
// reply goes, then the new message gets its own turn.
func TestRunPollerDebounced_MessageDuringTypingKeepsComposedReply(t *testing.T) {
	rt := &recordingRuntime{}
	second := make(chan struct{})
	tg := &gatedTelegram{sequentialTelegram: sequentialTelegram{batches: [][]TelegramUpdate{
		{{UpdateID: 1, ChatID: 7, MessageID: 1, Text: "привет"}},
		{{UpdateID: 2, ChatID: 7, MessageID: 2, Text: "хочу с вами работать"}},
	}}, gates: []chan struct{}{nil, second}}
	pacing := &PacingConfig{BaseQuiet: 30 * time.Millisecond, MaxQuiet: time.Second, CharsPerMinute: 250, MinTyping: 400 * time.Millisecond, MaxTyping: 400 * time.Millisecond}
	cfg := DebounceConfig{QuietWindow: 30 * time.Millisecond, MaxWindow: 5 * time.Second, Pacing: pacing}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runPollerDebounced(ctx, tg, fakeA2A{}, rt, Binding{AgentName: "agent-typing", BotToken: "tok"}, &cfg)

	if !waitLong(t, 5*time.Second, func() bool { return rt.callCount() == 1 }) {
		t.Fatal("run 1 never reached the agent")
	}
	time.Sleep(50 * time.Millisecond)
	close(second)

	if !waitLong(t, 5*time.Second, func() bool { return tg.sentCount() == 2 }) {
		t.Fatalf("expected the composed reply and then the new turn's reply, got %v", tg.sent)
	}
	rt.mu.Lock()
	calls := append([]RuntimeMessageRequest(nil), rt.calls...)
	rt.mu.Unlock()
	if len(calls) != 2 || len(calls[1].Messages) != 1 || calls[1].Messages[0].Content != "хочу с вами работать" {
		t.Fatalf("the second turn must carry only the new message, got %+v", calls)
	}
}

// TestDebounceConfigFromEnv_AlwaysBatches: batching is not an opt-in. With
// no env at all the gateway still merges a burst on the default windows,
// so a lost TG_GATEWAY_PACING can never bring back one-reply-per-message.
func TestDebounceConfigFromEnv_AlwaysBatches(t *testing.T) {
	for _, key := range []string{"TG_GATEWAY_PACING", "TG_GATEWAY_DEBOUNCE_QUIET_MS", "TG_GATEWAY_DEBOUNCE_MAX_MS"} {
		t.Setenv(key, "")
	}
	cfg := DebounceConfigFromEnv()
	if cfg == nil {
		t.Fatal("debounce must be on by default")
	}
	if cfg.QuietWindow != 0 || cfg.MaxWindow != 0 || cfg.Pacing != nil {
		t.Fatalf("bare env must leave the defaults to NewDebouncer, got %+v", cfg)
	}
	t.Setenv("TG_GATEWAY_DEBOUNCE_QUIET_MS", "2500")
	t.Setenv("TG_GATEWAY_DEBOUNCE_MAX_MS", "8000")
	t.Setenv("TG_GATEWAY_PACING", "1")
	cfg = DebounceConfigFromEnv()
	if cfg.QuietWindow != 2500*time.Millisecond || cfg.MaxWindow != 8*time.Second || cfg.Pacing == nil {
		t.Fatalf("env overrides must land, got %+v", cfg)
	}
}

// TestInterrupt_CancelUnclaimedHandsBackTheBatchOnce: the poll loop's cancel
// takes the unfinished run's batch exactly once; a second message landing
// while that run is still unwinding carries nothing extra, and the run's
// own claim is refused so it cannot answer a batch that is being re-run.
func TestInterrupt_CancelUnclaimedHandsBackTheBatchOnce(t *testing.T) {
	s := newInterruptState()
	ctx := context.Background()
	batch := []TelegramUpdate{{MessageID: 1, Text: "привет"}}
	runCtx, done, full, _ := s.begin("1", ctx, batch)
	defer done()
	if len(full) != 1 {
		t.Fatalf("first run carries nothing, got %v", full)
	}
	got := s.cancelUnclaimed("1")
	if len(got) != 1 || got[0].MessageID != 1 {
		t.Fatalf("cancel must hand the batch back, got %v", got)
	}
	if runCtx.Err() == nil {
		t.Fatal("the run must be canceled")
	}
	if again := s.cancelUnclaimed("1"); len(again) != 0 {
		t.Fatalf("the batch is handed out once, got %v", again)
	}
	if s.claimReply("1", runCtx) {
		t.Fatal("a canceled run must not win the reply claim")
	}
	if s.cancelUnclaimed("missing") != nil {
		t.Fatal("unknown chat is a no-op")
	}
}

// TestInterrupt_ClaimedRunIsNotCanceledAndNotCarried: once a run holds the
// reply claim, neither the poll loop's cancel nor a superseding begin may
// take it back -- the reply is composed, only the typing pause is cut.
func TestInterrupt_ClaimedRunIsNotCanceledAndNotCarried(t *testing.T) {
	s := newInterruptState()
	ctx := context.Background()
	batch := []TelegramUpdate{{MessageID: 1, Text: "привет"}}
	runCtx, done, _, _ := s.begin("1", ctx, batch)
	if !s.claimReply("1", runCtx) {
		t.Fatal("live run must win the claim")
	}
	if got := s.cancelUnclaimed("1"); got != nil || runCtx.Err() != nil {
		t.Fatalf("claimed run must stay alive, got %v err=%v", got, runCtx.Err())
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		done()
	}()
	_, done2, full, superseded := s.begin("1", ctx, []TelegramUpdate{{MessageID: 2, Text: "хочу"}})
	defer done2()
	if !superseded || len(full) != 1 || full[0].MessageID != 2 {
		t.Fatalf("supersede of a claimed run carries nothing, superseded=%v full=%v", superseded, full)
	}
}

// TestInterrupt_BeginCarriesUnclaimedBatch closes the flush-to-begin
// window: a run the poll loop never saw (it began after the new message
// was enqueued) still hands its batch to the run that supersedes it.
func TestInterrupt_BeginCarriesUnclaimedBatch(t *testing.T) {
	s := newInterruptState()
	ctx := context.Background()
	runCtx1, done1, _, _ := s.begin("1", ctx, []TelegramUpdate{{MessageID: 1, Text: "привет"}})
	go func() {
		<-runCtx1.Done()
		done1()
	}()
	runCtx2, done2, full, superseded := s.begin("1", ctx, []TelegramUpdate{{MessageID: 2, Text: "хочу"}})
	if !superseded {
		t.Fatal("run 1 must be superseded")
	}
	if len(full) != 2 || full[0].MessageID != 1 || full[1].MessageID != 2 {
		t.Fatalf("run 2 must answer run 1's batch ahead of its own, got %v", full)
	}
	if s.claimReply("1", runCtx1) {
		t.Fatal("run 1 must not claim after supersede")
	}
	go func() {
		<-runCtx2.Done()
		done2()
	}()
	_, done3, full3, _ := s.begin("1", ctx, []TelegramUpdate{{MessageID: 3, Text: "с вами работать"}})
	defer done3()
	if len(full3) != 3 || full3[0].MessageID != 1 || full3[2].MessageID != 3 {
		t.Fatalf("a run inherits the whole chain, got %v", full3)
	}
}
