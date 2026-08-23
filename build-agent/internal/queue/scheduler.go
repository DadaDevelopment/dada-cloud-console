// Package queue provides in-process build concurrency control and supersession.
package queue

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// Scheduler bounds concurrent builds with a buffered semaphore and tracks a
// per-build cancel func so newer commits on the same repo+branch can supersede
// older in-flight builds (Vercel behavior). This is real (small) logic.
type Scheduler struct {
	sem chan struct{}

	mu      sync.Mutex
	cancels map[uuid.UUID]context.CancelFunc
}

// New returns a Scheduler allowing at most maxConcurrent simultaneous builds.
func New(maxConcurrent int) *Scheduler {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Scheduler{
		sem:     make(chan struct{}, maxConcurrent),
		cancels: make(map[uuid.UUID]context.CancelFunc),
	}
}

// Acquire blocks until a concurrency slot is free or ctx is done. On success it
// registers a cancelable child context for the build and returns it; the caller
// MUST call the returned release func when the build finishes.
func (s *Scheduler) Acquire(ctx context.Context, buildID uuid.UUID) (context.Context, func(), bool) {
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, false
	}

	buildCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancels[buildID] = cancel
	s.mu.Unlock()

	release := func() {
		s.mu.Lock()
		delete(s.cancels, buildID)
		s.mu.Unlock()
		cancel()
		<-s.sem
	}
	return buildCtx, release, true
}

// Cancel aborts an in-flight build by id, if tracked. Returns true if a build
// was found and signaled. Used by supersession.
func (s *Scheduler) Cancel(buildID uuid.UUID) bool {
	s.mu.Lock()
	cancel, ok := s.cancels[buildID]
	s.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// Tracked reports whether buildID already holds a slot in this process. Callers
// that scan for orphaned/in-flight DB rows (Reconcile) must check this before
// Acquire, or a build already running in another goroutine gets a second
// concurrent attach/finalize racing the first.
func (s *Scheduler) Tracked(buildID uuid.UUID) bool {
	s.mu.Lock()
	_, ok := s.cancels[buildID]
	s.mu.Unlock()
	return ok
}

// Inflight returns the number of builds currently holding a slot.
func (s *Scheduler) Inflight() int {
	return len(s.sem)
}
