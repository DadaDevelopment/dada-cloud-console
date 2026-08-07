package api

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testNonNilPool builds a pool that is never dialed: the code paths under test
// return before touching the database, and the nil-pool shortcut in
// runWithAdvisoryLock would otherwise hide what they are meant to prove.
func testNonNilPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatalf("parse placeholder dsn: %v", err)
	}
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create placeholder pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestAdvisoryLockHolderBudgetLeavesPoolHeadroom guards the invariant that
// caused a 97-minute prod outage on 2026-08-06: the background loops must not
// be able to pin every connection in the pool, because each of them queries
// that same pool from inside its own critical section and would then wait for
// a connection only another blocked holder could return.
//
// Needs no database: the failure was a relationship between two constants, and
// this is the assertion that keeps them in that relationship.
func TestAdvisoryLockHolderBudgetLeavesPoolHeadroom(t *testing.T) {
	if got := cap(advisoryLockSlots); got != maxConcurrentAdvisoryLockHolders {
		t.Fatalf("advisoryLockSlots capacity = %d, want %d", got, maxConcurrentAdvisoryLockHolders)
	}
	if maxConcurrentAdvisoryLockHolders >= int(db.DefaultMaxConns) {
		t.Fatalf("holder budget %d must stay below pool size %d, or the loops can starve the pool",
			maxConcurrentAdvisoryLockHolders, db.DefaultMaxConns)
	}
}

// TestRunWithAdvisoryLockRespectsCanceledContext covers the entry path added
// with the holder budget: a tick whose context is already done must give the
// slot back rather than queue behind holders that outlive it.
func TestRunWithAdvisoryLockRespectsCanceledContext(t *testing.T) {
	for i := 0; i < maxConcurrentAdvisoryLockHolders; i++ {
		advisoryLockSlots <- struct{}{}
	}
	t.Cleanup(func() {
		for i := 0; i < maxConcurrentAdvisoryLockHolders; i++ {
			<-advisoryLockSlots
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var ran atomic.Bool
	done := make(chan bool, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		done <- runWithAdvisoryLock(ctx, testNonNilPool(t), 0x7e57_0002, "canceled", func(context.Context) {
			ran.Store(true)
		})
	}()

	select {
	case got := <-done:
		if got {
			t.Fatalf("runWithAdvisoryLock reported ran=true on a canceled context")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("runWithAdvisoryLock blocked on a full holder budget instead of honoring context cancellation")
	}
	wg.Wait()
	if ran.Load() {
		t.Fatalf("loop body ran on a canceled context")
	}
}
