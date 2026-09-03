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
// overlap (their replies and cancels never interleave).
type chatRun struct {
	mu     sync.Mutex
	gen    int
	cancel context.CancelFunc
	doneCh chan struct{}
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
	runs map[int64]*chatRun
}

func newInterruptState() *interruptState {
	return &interruptState{runs: map[int64]*chatRun{}}
}

// begin registers a fresh run for the chat, superseding any active one: the
// old run's context is canceled and begin waits (bounded) for its done().
// The caller MUST call the returned done exactly once when its terminal
// bookkeeping (reply sent, or silence on cancel) is finished. superseded
// reports whether an active run was actually canceled -- for the debug log,
// not for control flow.
func (s *interruptState) begin(chatID int64, parent context.Context) (runCtx context.Context, done func(), superseded bool) {
	s.mu.Lock()
	run, ok := s.runs[chatID]
	if !ok {
		run = &chatRun{}
		s.runs[chatID] = run
	}
	s.mu.Unlock()

	run.mu.Lock()
	if run.cancel != nil {
		run.cancel()
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

	return context.WithValue(ctx, runGenKey{}, myGen), done, superseded
}

// claimReply atomically grants the run identified by gen the right to send
// its reply. It returns false once a newer generation exists (the run was
// superseded mid-flight and must stay silent) or the run already finished
// (done() cleared cancel). Winning the claim is final: a supersede landing
// microseconds later cannot unsend -- the reply was fully computed before
// the correction arrived, and the new run restarts anyway.
func (s *interruptState) claimReply(chatID int64, runCtx context.Context) bool {
	gen, ok := runCtx.Value(runGenKey{}).(int)
	if !ok {
		return false
	}

	s.mu.Lock()
	run, ok := s.runs[chatID]
	s.mu.Unlock()
	if !ok {
		return false
	}

	run.mu.Lock()
	defer run.mu.Unlock()
	return run.gen == gen && run.cancel != nil
}

// forget drops the chat's run entry -- used on poller shutdown so a
// restarted poller starts clean rather than inheriting a cancel pointing at
// a dead goroutine's context.
func (s *interruptState) forget(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run, ok := s.runs[chatID]; ok {
		run.mu.Lock()
		if run.cancel != nil {
			run.cancel()
			run.cancel = nil
		}
		run.mu.Unlock()
		delete(s.runs, chatID)
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
