package tggateway

import (
	"context"
	"sync"
	"time"
)

// supersedeWaitCap bounds how long a new run waits for the canceled run to
// unwind. Cancel propagates instantly into an in-flight http.Client.Do
// through the request context, so the cap is generous -- it exists so a
// wedged transport can never deadlock the chat forever.
const supersedeWaitCap = 10 * time.Second

// runGenKey carries a run's generation through its context, so the reply
// gate can match the context to the chat's current generation without the
// caller threading an extra parameter.
type runGenKey struct{}

// chatRun is one chat's run state. mu guards everything. gen is the current
// generation: begin() bumps it, and a run whose generation no longer matches
// has been superseded. doneCh is closed by the owning run's done() exactly
// once; a superseding begin() waits on it so two runs of one chat never
// overlap (their replies and cancels never interleave). batch is what the
// run is answering, handed back by cancelUnclaimed so a message that lands
// mid-generation restarts the run over the whole thought instead of
// losing its first half. claimed flips once the run has won the right to
// send: from then on a new message can no longer unsend it. tail is the one
// exception (plan 3.1a): while a claimed run is still sending the later
// messages of a series, a new client message cancels what has not gone out
// yet -- the client has moved on, and a person would not keep typing out the
// rest of a thought the client already answered. What was already sent is
// never unsent.
type chatRun struct {
	mu      sync.Mutex
	gen     int
	cancel  context.CancelFunc
	doneCh  chan struct{}
	batch   []TelegramUpdate
	claimed bool
	tail    bool
}

// interruptState is the per-poller run tracking that implements the
// cancel_and_restart interrupt policy: one active agent run per chat, a new
// message cancels the in-flight run instead of queueing behind it. It
// replaces Step 2's plain per-chat mutex.
//
// The reply gate is a claim, not a check: claimReply atomically grants one
// run the right to send. A run whose HTTP call finished successfully just
// as a supersede landed can still win the claim and deliver its computed
// reply -- then the new run starts fresh and addresses the correction from
// the full history. The bad outcome (a superseded run replying AFTER the
// new run) is impossible by construction: the new run starts only after the
// old run's done() has closed doneCh, and a run sends strictly before its
// done().
type interruptState struct {
	mu   sync.Mutex
	runs map[string]*chatRun
}

func newInterruptState() *interruptState {
	return &interruptState{runs: map[string]*chatRun{}}
}

// begin registers a fresh run for the chat, superseding any active one: the
// old run's context is canceled and begin waits (bounded) for its done().
// The caller MUST call the returned done exactly once when its terminal
// bookkeeping (reply sent, or silence on cancel) is finished. full is the
// batch this run answers: the caller's batch, preceded by the superseded
// run's batch when that run had not yet claimed its reply, so a message
// that slipped in between the debouncer's flush and this begin (where
// cancelUnclaimed cannot see the run yet) still ends in one run over the
// whole thought. superseded reports whether an active run was actually
// canceled -- for the debug log, not for control flow.
func (s *interruptState) begin(convKey string, parent context.Context, batch []TelegramUpdate) (runCtx context.Context, done func(), full []TelegramUpdate, superseded bool) {
	s.mu.Lock()
	run, ok := s.runs[convKey]
	if !ok {
		run = &chatRun{}
		s.runs[convKey] = run
	}
	s.mu.Unlock()

	full = batch
	run.mu.Lock()
	if run.cancel != nil {
		run.cancel()
		if !run.claimed && len(run.batch) > 0 {
			full = append(append(make([]TelegramUpdate, 0, len(run.batch)+len(batch)), run.batch...), batch...)
		}
		run.batch = nil
		ch := run.doneCh
		run.mu.Unlock()
		superseded = true
		select {
		case <-ch:
		case <-time.After(supersedeWaitCap):
		}
		run.mu.Lock()
	}
	run.gen++
	ctx, cancel := context.WithCancel(parent)
	run.cancel = cancel
	run.doneCh = make(chan struct{})
	run.batch = full
	run.claimed = false
	run.tail = false
	myGen := run.gen
	myCh := run.doneCh
	run.mu.Unlock()

	done = func() {
		run.mu.Lock()
		defer run.mu.Unlock()
		if run.gen != myGen || run.doneCh != myCh {
			return
		}
		cancel()
		run.cancel = nil
		close(myCh)
	}

	return context.WithValue(ctx, runGenKey{}, myGen), done, full, superseded
}

// claimReply atomically grants the run identified by gen the right to send
// its reply. It returns false once a newer generation exists (the run was
// superseded mid-flight and must stay silent), the run already finished
// (done() cleared cancel), or cancelUnclaimed got there first (the context
// is canceled and the batch is back in the debouncer, so a reply now would
// be the first of two). Winning the claim is final: a supersede landing
// microseconds later cannot unsend -- the reply was fully computed before
// the correction arrived, and the new run restarts anyway.
func (s *interruptState) claimReply(convKey string, runCtx context.Context) bool {
	gen, ok := runCtx.Value(runGenKey{}).(int)
	if !ok {
		return false
	}

	s.mu.Lock()
	run, ok := s.runs[convKey]
	s.mu.Unlock()
	if !ok {
		return false
	}

	run.mu.Lock()
	defer run.mu.Unlock()
	if run.gen != gen || run.cancel == nil || runCtx.Err() != nil {
		return false
	}
	run.claimed = true
	return true
}

// markTail declares whether the run owning convKey still has unsent messages
// of a series. Only a split reply ever sets it; a single-message run leaves
// it false and behaves exactly as before.
func (s *interruptState) markTail(convKey string, pending bool) {
	s.mu.Lock()
	run, ok := s.runs[convKey]
	s.mu.Unlock()
	if !ok {
		return
	}
	run.mu.Lock()
	run.tail = pending
	run.mu.Unlock()
}

// cancelUnclaimed is the poll loop's half of the mid-generation restart: a
// new message for a chat whose run is still at the agent cancels that run
// and takes its batch back, so the caller can enqueue the old messages
// ahead of the new one and the chat gets ONE reply to the whole thought.
// A run that already claimed its reply is left alone -- it is only typing
// the answer out, and a new message must not unsend it. The batch is
// handed out once: a second message landing while the canceled run is
// still unwinding finds nothing to carry.
func (s *interruptState) cancelUnclaimed(convKey string) []TelegramUpdate {
	s.mu.Lock()
	run, ok := s.runs[convKey]
	s.mu.Unlock()
	if !ok {
		return nil
	}

	run.mu.Lock()
	defer run.mu.Unlock()
	if run.cancel == nil {
		return nil
	}
	if run.claimed {
		// A claimed run mid-series: drop the unsent tail, but do not carry
		// its batch back -- part of the answer is already in the chat, and
		// re-running the whole thought would duplicate it.
		if run.tail {
			run.tail = false
			run.cancel()
		}
		return nil
	}
	run.cancel()
	batch := run.batch
	run.batch = nil
	return batch
}

// forget drops the chat's run entry -- used on poller shutdown so a
// restarted poller starts clean rather than inheriting a cancel pointing at
// a dead goroutine's context.
func (s *interruptState) forget(convKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run, ok := s.runs[convKey]; ok {
		run.mu.Lock()
		if run.cancel != nil {
			run.cancel()
			run.cancel = nil
		}
		run.mu.Unlock()
		delete(s.runs, convKey)
	}
}

// forgetAll drops every chat's run entry -- the poller-shutdown bulk form.
func (s *interruptState) forgetAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, run := range s.runs {
		run.mu.Lock()
		if run.cancel != nil {
			run.cancel()
			run.cancel = nil
		}
		run.mu.Unlock()
		delete(s.runs, id)
	}
}
