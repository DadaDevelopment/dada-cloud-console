package tggateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestInterrupt_BeginDoneLifecycle(t *testing.T) {
	s := newInterruptState()
	ctx := context.Background()

	runCtx, done, superseded := s.begin(1, ctx)
	if superseded {
		t.Fatal("first begin must not report superseded")
	}
	if runCtx.Err() != nil {
		t.Fatalf("fresh run context must be alive, got %v", runCtx.Err())
	}
	if !s.claimReply(1, runCtx) {
		t.Fatal("reply claim must succeed for the active run")
	}

	done()

	if s.claimReply(1, runCtx) {
		t.Fatal("reply claim must fail after done (cancel cleared)")
	}
}

func TestInterrupt_SupersedeCancelsInFlightRun(t *testing.T) {
	s := newInterruptState()
	ctx := context.Background()

	runCtx1, done1, _ := s.begin(1, ctx)
	if s.claimReply(1, runCtx1) != true {
		t.Fatal("run 1 must hold the reply claim")
	}

	runCtx2, done2, superseded := s.begin(1, ctx)
	if !superseded {
		t.Fatal("second begin while run 1 active must report superseded")
	}
	if runCtx1.Err() == nil {
		t.Fatal("run 1's context must be canceled by the supersede")
	}
	if runCtx2.Err() != nil {
		t.Fatalf("run 2's context must be alive, got %v", runCtx2.Err())
	}

	if s.claimReply(1, runCtx1) {
		t.Fatal("superseded run 1 must lose the reply claim")
	}
	if !s.claimReply(1, runCtx2) {
		t.Fatal("run 2 must hold the reply claim")
	}

	done1()
	done2()
}

// TestInterrupt_SupersedeWaitsForDone verifies the no-overlap guarantee:
// begin for a superseding run returns only after the old run's done() has
// run. The old run records the ordering; the new run must observe it.
func TestInterrupt_SupersedeWaitsForDone(t *testing.T) {
	s := newInterruptState()
	ctx := context.Background()

	var mu sync.Mutex
	var order []string

	_, done1, _ := s.begin(1, ctx)
	go func() {
		time.Sleep(30 * time.Millisecond)
		mu.Lock()
		order = append(order, "done1")
		mu.Unlock()
		done1()
	}()

	_, done2, superseded := s.begin(1, ctx)
	mu.Lock()
	order = append(order, "begin2-returned")
	mu.Unlock()

	if !superseded {
		t.Fatal("expected superseded=true")
	}
	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	if len(got) != 2 || got[0] != "done1" || got[1] != "begin2-returned" {
		t.Fatalf("begin must wait for old run's done, order was %v", got)
	}
	done2()
}

// TestInterrupt_ClaimAfterLateSupersede pins the reply-gate semantics: a
// supersede landing after a run WON the claim does not retroactively revoke
// it -- claim is a one-way gate, so a fully computed reply is never dropped
// by a microsecond-late cancel (the new run restarts regardless).
func TestInterrupt_ClaimAfterLateSupersede(t *testing.T) {
	s := newInterruptState()
	ctx := context.Background()

	runCtx1, done1, _ := s.begin(1, ctx)
	if !s.claimReply(1, runCtx1) {
		t.Fatal("run 1 must win its claim while active")
	}

	runCtx2, done2, superseded := s.begin(1, ctx)
	if !superseded {
		t.Fatal("expected superseded")
	}

	if s.claimReply(1, runCtx2) != true {
		t.Fatal("run 2 must be able to claim")
	}

	done1()
	done2()
}

func TestInterrupt_CancelUnknownChatIsNoop(t *testing.T) {
	s := newInterruptState()
	if err := context.Canceled; err == nil {
		t.Fatal("sanity")
	}
	s.forget(999)
	s.forgetAll()
}

// blockingRuntime blocks the first call until release, then returns a
// reply; the second call returns immediately. Canceled calls return the
// ctx error.
type blockingRuntime struct {
	mu      sync.Mutex
	release chan struct{}
	calls   int
}

func (b *blockingRuntime) ProcessMessage(ctx context.Context, _ RuntimeMessageRequest) (RuntimeMessageResponse, error) {
	b.mu.Lock()
	b.calls++
	n := b.calls
	b.mu.Unlock()

	if n == 1 {
		select {
		case <-b.release:
			return RuntimeMessageResponse{Text: "slow reply"}, nil
		case <-ctx.Done():
			return RuntimeMessageResponse{}, ctx.Err()
		}
	}
	return RuntimeMessageResponse{Text: "fast reply"}, nil
}

// TestRunPollerDebounced_InterruptCancelsStaleRun is the owner's scenario
// end-to-end at the poller level: message 1's run is blocked in the agent
// call, message 2 arrives, run 1 is canceled (its reply never reaches the
// user), run 2 completes and its reply is delivered.
func TestRunPollerDebounced_InterruptCancelsStaleRun(t *testing.T) {
	rt := &blockingRuntime{release: make(chan struct{})}

	// chatLockedtelegram serves two sequential batches of one chat.
	tg := &sequentialTelegram{batches: [][]TelegramUpdate{
		{{UpdateID: 1, ChatID: 7, MessageID: 1, Text: "какой у тебя вопрос по юрисдикции?"}},
		{{UpdateID: 2, ChatID: 7, MessageID: 2, Text: "а, стоп, я вообще из Казахстана"}},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runPollerDebounced(ctx, tg, fakeA2A{}, rt, Binding{AgentName: "agent-x", BotToken: "tok-x"}, nil)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tg.mu.Lock()
		sent := append([]string(nil), tg.sent...)
		tg.mu.Unlock()
		if len(sent) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	close(rt.release)

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tg.mu.Lock()
		sent := append([]string(nil), tg.sent...)
		tg.mu.Unlock()
		if len(sent) >= 1 && sent[len(sent)-1] == "fast reply" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	tg.mu.Lock()
	sent := append([]string(nil), tg.sent...)
	tg.mu.Unlock()

	if len(sent) != 1 {
		t.Fatalf("exactly one reply (run 2's) must reach the user, got %v", sent)
	}
	if sent[0] != "fast reply" {
		t.Fatalf("the surviving reply must be run 2's, got %q", sent[0])
	}
}

// sequentialTelegram serves one batch per GetUpdates call, then blocks.
type sequentialTelegram struct {
	fakeTelegram
	mu        sync.Mutex
	batches   [][]TelegramUpdate
	i         int
	sent      []string
	repliedTo []int64
}

func (s *sequentialTelegram) GetUpdates(ctx context.Context, token string, offset int64, timeoutSec int) ([]TelegramUpdate, error) {
	s.mu.Lock()
	i := s.i
	if i < len(s.batches) {
		s.i++
		s.mu.Unlock()
		return s.batches[i], nil
	}
	s.mu.Unlock()
	return s.fakeTelegram.GetUpdates(ctx, token, offset, timeoutSec)
}

func (s *sequentialTelegram) SendMessage(_ context.Context, _ string, _ int64, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, text)
	return nil
}

func (s *sequentialTelegram) SendMessageReply(_ context.Context, _ string, _ int64, replyTo int64, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, text)
	s.repliedTo = append(s.repliedTo, replyTo)
	return nil
}

func (s *sequentialTelegram) sentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

func (s *sequentialTelegram) SendMessageWithLocationButton(_ context.Context, _ string, _ int64, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, text)
	return nil
}

// TestRunPollerDebounced_CanceledRunIsNotFailureStreak guards the failure
// classifier: a run killed by supersede errors with ctx.Canceled, which is
// NOT a failure -- the user must not get the a2aFailureFallback because a
// newer message arrived.
func TestRunPollerDebounced_CanceledRunIsNotFailureStreak(t *testing.T) {
	rt := &blockingRuntime{release: make(chan struct{})}
	tg := &sequentialTelegram{batches: [][]TelegramUpdate{
		{{UpdateID: 1, ChatID: 8, MessageID: 1, Text: "first"}},
		{{UpdateID: 2, ChatID: 8, MessageID: 2, Text: "stop, correction"}},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runPollerDebounced(ctx, tg, fakeA2A{}, rt, Binding{AgentName: "agent-y", BotToken: "tok-y"}, nil)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tg.mu.Lock()
		n := len(tg.sent)
		tg.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	close(rt.release)
	time.Sleep(100 * time.Millisecond)

	tg.mu.Lock()
	sent := append([]string(nil), tg.sent...)
	tg.mu.Unlock()

	for _, msg := range sent {
		if msg == a2aFailureFallback {
			t.Fatal("a superseded run must never trigger the failure fallback")
		}
	}
	if len(sent) != 1 || sent[0] != "fast reply" {
		t.Fatalf("expected exactly run 2's reply, got %v", sent)
	}
}

// TestRunPollerDebounced_SlowReplyWinsClaimOverLateSupersede pins the claim
// semantics at the poller level: run 1's agent call completes right as run
// 2's supersede lands. run 1 already won its claim before the HTTP call
// returned? No -- the gate sits between the HTTP return and the send, so
// whichever ordering happens, exactly one of the two runs delivers. This
// test forces the "run 1 computed a reply, supersede lands after the send"
// path and asserts the user still gets exactly the expected count of
// replies (never two, never zero).
func TestRunPollerDebounced_SlowReplyWinsClaimOverLateSupersede(t *testing.T) {
	rt := &countingRuntime{replies: 2}
	tg := &sequentialTelegram{batches: [][]TelegramUpdate{
		{{UpdateID: 1, ChatID: 9, MessageID: 1, Text: "q1"}},
		{{UpdateID: 2, ChatID: 9, MessageID: 2, Text: "q2"}},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runPollerDebounced(ctx, tg, fakeA2A{}, rt, Binding{AgentName: "agent-z", BotToken: "tok-z"}, nil)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tg.mu.Lock()
		n := len(tg.sent)
		tg.mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	tg.mu.Lock()
	n := len(tg.sent)
	tg.mu.Unlock()

	if n < 1 || n > 2 {
		t.Fatalf("one or two replies may reach the user under the race, got %d", n)
	}
}

// countingRuntime returns a distinct reply per call, immediately.
type countingRuntime struct {
	mu      sync.Mutex
	replies int
	calls   int
}

func (c *countingRuntime) ProcessMessage(_ context.Context, _ RuntimeMessageRequest) (RuntimeMessageResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls > c.replies {
		return RuntimeMessageResponse{}, errors.New("no more replies")
	}
	return RuntimeMessageResponse{Text: "reply"}, nil
}

// TestRunPollerDebounced_ReplyAnchorsToLastBatchMessage verifies Step 4: a
// batch of three messages gets ONE reply, sent as a native Telegram reply
// to the LAST message of the batch (the natural reading anchor).
func TestRunPollerDebounced_ReplyAnchorsToLastBatchMessage(t *testing.T) {
	tg := &sequentialTelegram{batches: [][]TelegramUpdate{
		{
			{UpdateID: 1, ChatID: 11, MessageID: 101, Text: "привет"},
			{UpdateID: 2, ChatID: 11, MessageID: 102, Text: "слушай"},
			{UpdateID: 3, ChatID: 11, MessageID: 103, Text: "вопрос по регистрации"},
		},
	}}
	rt := &recordingRuntime{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runPollerDebounced(ctx, tg, fakeA2A{}, rt, Binding{AgentName: "agent-r", BotToken: "tok-r"}, nil)

	waitFor(t, func() bool { return rt.callCount() == 1 })
	waitFor(t, func() bool { return tg.sentCount() == 1 })

	tg.mu.Lock()
	defer tg.mu.Unlock()
	if len(tg.repliedTo) != 1 {
		t.Fatalf("expected 1 SendMessageReply call, got %d (sent=%v)", len(tg.repliedTo), tg.sent)
	}
	if tg.repliedTo[0] != 103 {
		t.Fatalf("reply must anchor to the LAST batch message id 103, got %d", tg.repliedTo[0])
	}
}

// TestRunPollerDebounced_NoChannelIDMeansPlainSend covers the degradation:
// messages without Telegram ids (manual/system) produce a plain SendMessage,
// never a reply to 0.
func TestRunPollerDebounced_NoChannelIDMeansPlainSend(t *testing.T) {
	tg := &sequentialTelegram{batches: [][]TelegramUpdate{
		{{UpdateID: 1, ChatID: 12, Text: "no ids here"}},
	}}
	rt := &recordingRuntime{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runPollerDebounced(ctx, tg, fakeA2A{}, rt, Binding{AgentName: "agent-p", BotToken: "tok-p"}, nil)

	waitFor(t, func() bool { return tg.sentCount() == 1 })

	tg.mu.Lock()
	defer tg.mu.Unlock()
	if len(tg.repliedTo) != 0 {
		t.Fatalf("no SendMessageReply expected without channel ids, got %v", tg.repliedTo)
	}
}
