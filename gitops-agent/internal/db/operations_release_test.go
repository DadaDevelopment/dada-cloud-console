package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestReleaseHeldClaimsHandsBackWhatTheProcessNeverRan covers the rollout that
// lands mid-batch: a claim takes up to claimBatchSize operations at once and the
// dispatcher works through them one at a time, so everything still queued behind
// the current one was marked Processing without a single worker ever starting it.
// Before the release path those rows waited out staleProcessingTimeout -- half an
// hour in which the console showed a user's deploy as running and nothing was
// running it.
func TestReleaseHeldClaimsHandsBackWhatTheProcessNeverRan(t *testing.T) {
	ctx, pool := releaseTestPool(t)
	projectID, envID := seedReleaseProject(t, ctx, pool)

	pending := seedOperationWithAge(t, ctx, pool, projectID, envID, "release-pending", "Created", time.Minute)
	defer execReap(t, ctx, pool, `DELETE FROM operations WHERE id = $1`, pending)

	if _, err := ClaimPending(ctx, pool); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got := operationStatus(t, ctx, pool, pending); got != "Processing" {
		t.Fatalf("claim left the operation %s, want Processing", got)
	}

	released, err := ReleaseHeldClaims(ctx, pool)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if released == 0 {
		t.Fatal("shutdown released no operations while holding a claim")
	}
	if got := operationStatus(t, ctx, pool, pending); got != "Created" {
		t.Fatalf("operation stayed %s after shutdown, want Created (it would wait out staleProcessingTimeout)", got)
	}
}

// TestReleaseHeldClaimsLeavesFinishedOperationsAlone pins the half that makes
// the release safe: an operation that reached a terminal status keeps its
// verdict. Releasing it would resurrect finished work -- a committed deploy
// rendered a second time, a failed one losing its error.
//
// The claim is registered by hand rather than through MarkFailed, because
// MarkFailed drops the claim in memory and the row would never reach the
// UPDATE. What is under test here is the database-side guard, the one that
// still holds when a handler finishes in the window between the shutdown
// taking its snapshot and the UPDATE landing.
func TestReleaseHeldClaimsLeavesFinishedOperationsAlone(t *testing.T) {
	ctx, pool := releaseTestPool(t)
	projectID, envID := seedReleaseProject(t, ctx, pool)

	done := seedOperationWithAge(t, ctx, pool, projectID, envID, "release-done", "Failed", time.Minute)
	defer execReap(t, ctx, pool, `DELETE FROM operations WHERE id = $1`, done)

	holdClaim(done)
	released, err := ReleaseHeldClaims(ctx, pool)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if released != 0 {
		t.Fatalf("shutdown moved %d finished operations, want 0", released)
	}
	if got := operationStatus(t, ctx, pool, done); got != "Failed" {
		t.Fatalf("shutdown reopened a finished operation as %s, want Failed", got)
	}
}

// TestTerminalWritesDropTheClaim covers the bookkeeping the guard above cannot:
// an operation that ended must leave the held set, or every shutdown drags the
// whole history of this process's finished work through a pointless UPDATE and
// the set grows without bound in a process that runs for weeks.
func TestTerminalWritesDropTheClaim(t *testing.T) {
	ctx, pool := releaseTestPool(t)
	projectID, envID := seedReleaseProject(t, ctx, pool)

	op := seedOperationWithAge(t, ctx, pool, projectID, envID, "release-terminal", "Created", time.Minute)
	defer execReap(t, ctx, pool, `DELETE FROM operations WHERE id = $1`, op)
	defer execReap(t, ctx, pool, `DELETE FROM audit_events WHERE operation_id = $1`, op)

	if _, err := ClaimPending(ctx, pool); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !claimHeld(op) {
		t.Fatal("a claimed operation was not registered as held")
	}
	if err := MarkFailed(ctx, pool, op, "PROCESSING_ERROR", "boom"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if claimHeld(op) {
		t.Fatal("a finished operation is still registered as held")
	}
}

func claimHeld(id uuid.UUID) bool {
	held.Lock()
	defer held.Unlock()
	_, ok := held.ids[id]
	return ok
}

func releaseTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	ensureSchemaForTest(t, ctx, pool)
	return ctx, pool
}

func seedReleaseProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	projectID := uuid.New()
	execReap(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, "p-"+projectID.String()[:8])
	envID := seedPreviewEnv(t, ctx, pool, projectID, false, time.Now().Add(time.Hour))
	return projectID, envID
}

func operationStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM operations WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("read operation status: %v", err)
	}
	return status
}
