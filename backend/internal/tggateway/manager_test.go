package tggateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeStore is an in-memory Store so Reconcile can be tested without a real
// Postgres, per the design doc's explicit "no real long-poll/network in CI"
// testing requirement.
type fakeStore struct {
	mu   sync.Mutex
	rows map[string]Binding
}

func newFakeStore() *fakeStore { return &fakeStore{rows: map[string]Binding{}} }

func (f *fakeStore) List(context.Context) ([]Binding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Binding, 0, len(f.rows))
	for _, b := range f.rows {
		out = append(out, b)
	}
	return out, nil
}

func (f *fakeStore) Get(_ context.Context, agentName string) (Binding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.rows[agentName]
	if !ok {
		return Binding{}, ErrNotFound
	}
	return b, nil
}

func (f *fakeStore) Upsert(_ context.Context, b Binding) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[b.AgentName] = b
	return nil
}

func (f *fakeStore) Delete(_ context.Context, agentName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, agentName)
	return nil
}

// fakeTelegram never touches the network: GetMe always succeeds, GetUpdates
// blocks until ctx is cancelled (a poller with nothing to do should not spin).
type fakeTelegram struct{}

func (fakeTelegram) GetMe(context.Context, string) (string, error) { return "fake_bot", nil }

func (fakeTelegram) GetUpdates(ctx context.Context, _ string, _ int64, _ int) ([]TelegramUpdate, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (fakeTelegram) SendMessage(context.Context, string, int64, string) error { return nil }

func (fakeTelegram) SendChatAction(context.Context, string, int64, string) error { return nil }

type fakeA2A struct{}

func (fakeA2A) Send(context.Context, string, string) (string, error) { return "ok", nil }

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met before deadline")
}

func TestReconcile_StartsPollerForNewRow(t *testing.T) {
	store := newFakeStore()
	mgr := NewManager(store, fakeTelegram{}, fakeA2A{})
	ctx := context.Background()

	if err := store.Upsert(ctx, Binding{AgentName: "agent-a", BotToken: "tok-a", Status: StatusActive}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := mgr.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	waitFor(t, func() bool { return mgr.liveCount() == 1 })
}

func TestReconcile_StopsPollerForRemovedRow(t *testing.T) {
	store := newFakeStore()
	mgr := NewManager(store, fakeTelegram{}, fakeA2A{})
	ctx := context.Background()

	if err := store.Upsert(ctx, Binding{AgentName: "agent-b", BotToken: "tok-b", Status: StatusActive}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := mgr.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	waitFor(t, func() bool { return mgr.liveCount() == 1 })

	if err := store.Delete(ctx, "agent-b"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := mgr.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	waitFor(t, func() bool { return mgr.liveCount() == 0 })
}

func TestReconcile_RestartsPollerOnTokenChange(t *testing.T) {
	store := newFakeStore()
	mgr := NewManager(store, fakeTelegram{}, fakeA2A{})
	ctx := context.Background()

	if err := store.Upsert(ctx, Binding{AgentName: "agent-c", BotToken: "tok-1", Status: StatusActive}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := mgr.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	waitFor(t, func() bool { return mgr.liveCount() == 1 })

	if err := store.Upsert(ctx, Binding{AgentName: "agent-c", BotToken: "tok-2", Status: StatusActive}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := mgr.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	waitFor(t, func() bool { return mgr.liveCount() == 1 })

	mgr.mu.Lock()
	token := mgr.pollers["agent-c"].token
	mgr.mu.Unlock()
	if token != "tok-2" {
		t.Fatalf("expected poller to pick up new token, got %q", token)
	}
}

func TestBind_ValidatesTokenAndStartsPoller(t *testing.T) {
	store := newFakeStore()
	mgr := NewManager(store, fakeTelegram{}, fakeA2A{})
	ctx := context.Background()

	b, err := mgr.Bind(ctx, "agent-d", "proj-1", "tok-d")
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if b.BotUsername != "fake_bot" {
		t.Fatalf("expected fake_bot username, got %q", b.BotUsername)
	}
	waitFor(t, func() bool { return mgr.liveCount() == 1 })

	stored, err := store.Get(ctx, "agent-d")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.BotToken != "tok-d" {
		t.Fatalf("expected token persisted, got %q", stored.BotToken)
	}
}

// rejectingTelegram fails GetMe, simulating a bad bot token.
type rejectingTelegram struct{ fakeTelegram }

func (rejectingTelegram) GetMe(context.Context, string) (string, error) {
	return "", context.DeadlineExceeded
}

func TestBind_RejectsInvalidToken(t *testing.T) {
	store := newFakeStore()
	mgr := NewManager(store, rejectingTelegram{}, fakeA2A{})

	_, err := mgr.Bind(context.Background(), "agent-e", "proj-1", "bad-token")
	if err == nil {
		t.Fatalf("expected error for invalid token")
	}
	var invalid ErrInvalidToken
	if !errors.As(err, &invalid) {
		t.Fatalf("expected ErrInvalidToken, got %v (%T)", err, err)
	}
	if mgr.liveCount() != 0 {
		t.Fatalf("expected no poller started for rejected token")
	}
}

// onceTelegram returns a fixed batch of updates on its first GetUpdates
// call, then blocks like fakeTelegram -- a poller loop needs exactly one
// pass over the batch to exercise the a2a-failure path without spinning.
// SendMessage calls are recorded so tests can assert on what the user
// actually received.
type onceTelegram struct {
	fakeTelegram
	mu      sync.Mutex
	updates []TelegramUpdate
	served  bool
	sent    []string
}

func (o *onceTelegram) GetUpdates(ctx context.Context, token string, offset int64, timeoutSec int) ([]TelegramUpdate, error) {
	o.mu.Lock()
	if !o.served {
		o.served = true
		batch := o.updates
		o.mu.Unlock()
		return batch, nil
	}
	o.mu.Unlock()
	return o.fakeTelegram.GetUpdates(ctx, token, offset, timeoutSec)
}

func (o *onceTelegram) SendMessage(_ context.Context, _ string, _ int64, text string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sent = append(o.sent, text)
	return nil
}

func (o *onceTelegram) sentCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.sent)
}

// failingA2A errors on Send until failures calls have happened, then
// succeeds -- lets a test drive an exact-length failure streak.
type failingA2A struct {
	mu       sync.Mutex
	failures int
	calls    int
}

func (f *failingA2A) Send(context.Context, string, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failures {
		return "", errors.New("a2a unreachable")
	}
	return "ok", nil
}

// TestRunPoller_WarnsOnFirstFailureNotAfterDelay guards the 2026-08-30
// production incident: a single a2a.Send failure used to be silent for 30s
// before the user got any reply, and getUpdates already advances past the
// failed message with no retry -- so on a one-off failure the user got
// nothing at all. The fix warns on the first failure of a streak.
func TestRunPoller_WarnsOnFirstFailureNotAfterDelay(t *testing.T) {
	tg := &onceTelegram{updates: []TelegramUpdate{{UpdateID: 1, ChatID: 42, Text: "hi"}}}
	a2a := &failingA2A{failures: 1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runPoller(ctx, tg, a2a, NewNoopRuntimeClient(), Binding{AgentName: "agent-g", BotToken: "tok-g"})

	waitFor(t, func() bool { return tg.sentCount() == 1 })
	if got := tg.sent[0]; got != a2aFailureFallback {
		t.Fatalf("expected fallback message %q, got %q", a2aFailureFallback, got)
	}
}

// TestRunPoller_DoesNotDuplicateWarningWithinSameFailureStreak keeps the
// original "warn once" design doc intent: several updates failing in the
// same streak must not each trigger their own message.
func TestRunPoller_DoesNotDuplicateWarningWithinSameFailureStreak(t *testing.T) {
	tg := &onceTelegram{updates: []TelegramUpdate{
		{UpdateID: 1, ChatID: 42, Text: "one"},
		{UpdateID: 2, ChatID: 42, Text: "two"},
		{UpdateID: 3, ChatID: 42, Text: "three"},
	}}
	a2a := &failingA2A{failures: 3}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runPoller(ctx, tg, a2a, NewNoopRuntimeClient(), Binding{AgentName: "agent-h", BotToken: "tok-h"})

	waitFor(t, func() bool { return tg.sentCount() >= 1 })
	time.Sleep(50 * time.Millisecond)
	if got := tg.sentCount(); got != 1 {
		t.Fatalf("expected exactly 1 fallback message for a 3-failure streak, got %d", got)
	}
}

func TestUnbind_StopsPollerAndRemovesRow(t *testing.T) {
	store := newFakeStore()
	mgr := NewManager(store, fakeTelegram{}, fakeA2A{})
	ctx := context.Background()

	if _, err := mgr.Bind(ctx, "agent-f", "proj-1", "tok-f"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	waitFor(t, func() bool { return mgr.liveCount() == 1 })

	if err := mgr.Unbind(ctx, "agent-f"); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	waitFor(t, func() bool { return mgr.liveCount() == 0 })

	if _, err := store.Get(ctx, "agent-f"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after unbind, got %v", err)
	}
}
