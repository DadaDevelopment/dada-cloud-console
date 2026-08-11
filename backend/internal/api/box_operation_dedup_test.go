package api

import (
	"errors"
	"testing"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
)

// TestEnqueueBoxReaperOperation_DedupSkipsRepeatedTicks reproduces the 2026-08-11
// ephprobe1 incident: a box stuck in Ready with a SuspendBox operation that never
// reaches a terminal status, and the reaper calling enqueueBoxReaperOperation
// again on every tick because nothing told it one was already in flight. Before
// the dedup gate this loop planted a new operations row every call; it must now
// plant exactly one, no matter how many times the reaper ticks over the box.
func TestEnqueueBoxReaperOperation_DedupSkipsRepeatedTicks(t *testing.T) {
	pool := testOptimisticPool(t)
	ctx, projectID, envID := seedBoxAuditProject(t, pool)
	boxID := uuid.New()
	boxName := "ephprobe-" + uuid.NewString()[:8]

	firstOpID, err := enqueueBoxReaperOperation(ctx, pool, projectID, envID, boxName,
		models.ActionSuspendBox, "ttl", models.SuspendBoxPayload{BoxID: boxID, Reason: "ttl"})
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}

	for tick := 0; tick < 5; tick++ {
		_, err := enqueueBoxReaperOperation(ctx, pool, projectID, envID, boxName,
			models.ActionSuspendBox, "ttl", models.SuspendBoxPayload{BoxID: boxID, Reason: "ttl"})
		if !errors.Is(err, errBoxOperationAlreadyPending) {
			t.Fatalf("tick %d: err = %v, want errBoxOperationAlreadyPending", tick, err)
		}
	}

	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionSuspendBox); n != 1 {
		t.Fatalf("operations rows for %s = %d, want exactly 1 -- this is the crash-loop amplifier", boxName, n)
	}

	var auditCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events WHERE operation_id = $1`,
		firstOpID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit rows joined to the accepted operation = %d, want exactly 1", auditCount)
	}

	// Skipped ticks must be silent in the trail, not just in operations: the five
	// skipped calls above named the same box/action, and each is a candidate for
	// writeAuditRow to have been reached anyway (Outcome recorded pending or
	// success regardless of who wins the race). One accepted enqueue must
	// correspond to exactly one audit row for this resource, full stop.
	var totalForBox int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events
		  WHERE resource_kind = $1 AND resource_name = $2 AND action = $3`,
		models.ResourceKindBox, boxName, models.ActionSuspendBox,
	).Scan(&totalForBox); err != nil {
		t.Fatalf("count audit rows by resource: %v", err)
	}
	if totalForBox != 1 {
		t.Fatalf("audit rows for box %s/%s = %d, want 1 -- every skipped tick must be silent, not audited", boxName, models.ActionSuspendBox, totalForBox)
	}
}

// TestEnqueueBoxReaperOperation_AllowsRetryAfterTerminal proves the dedup gate is
// not one-way. If it blocked forever once a box had ANY SuspendBox in its
// history, a box whose first suspend genuinely failed would never be retried and
// would sleep in Ready forever -- the mirror-image bug to the one this gate
// fixes, and worse, because it has no crash loop to make it visible.
func TestEnqueueBoxReaperOperation_AllowsRetryAfterTerminal(t *testing.T) {
	pool := testOptimisticPool(t)
	ctx, projectID, envID := seedBoxAuditProject(t, pool)
	boxID := uuid.New()
	boxName := "retry-" + uuid.NewString()[:8]

	firstOpID, err := enqueueBoxReaperOperation(ctx, pool, projectID, envID, boxName,
		models.ActionSuspendBox, "ttl", models.SuspendBoxPayload{BoxID: boxID, Reason: "ttl"})
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}

	if _, err := enqueueBoxReaperOperation(ctx, pool, projectID, envID, boxName,
		models.ActionSuspendBox, "ttl", models.SuspendBoxPayload{BoxID: boxID, Reason: "ttl"}); !errors.Is(err, errBoxOperationAlreadyPending) {
		t.Fatalf("enqueue while first is still open: err = %v, want errBoxOperationAlreadyPending", err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE operations SET status = 'Failed' WHERE id = $1`, firstOpID); err != nil {
		t.Fatalf("mark first operation terminal: %v", err)
	}

	secondOpID, err := enqueueBoxReaperOperation(ctx, pool, projectID, envID, boxName,
		models.ActionSuspendBox, "ttl", models.SuspendBoxPayload{BoxID: boxID, Reason: "ttl"})
	if err != nil {
		t.Fatalf("enqueue after terminal: %v", err)
	}
	if secondOpID == firstOpID {
		t.Fatal("second enqueue returned the same operation id as the first")
	}

	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionSuspendBox); n != 2 {
		t.Fatalf("operations rows for %s = %d, want exactly 2 -- one failed, one retried", boxName, n)
	}
}

// TestEnqueueBoxReaperOperation_DedupIsPerAction proves the key does not conflate
// SuspendBox and DeleteBox for the same box: a pending suspend must not block the
// delete the reaper later needs to queue once that suspend lands and the box goes
// on to sleep out its 72h.
func TestEnqueueBoxReaperOperation_DedupIsPerAction(t *testing.T) {
	pool := testOptimisticPool(t)
	ctx, projectID, envID := seedBoxAuditProject(t, pool)
	boxID := uuid.New()
	boxName := "peraction-" + uuid.NewString()[:8]

	if _, err := enqueueBoxReaperOperation(ctx, pool, projectID, envID, boxName,
		models.ActionSuspendBox, "ttl", models.SuspendBoxPayload{BoxID: boxID, Reason: "ttl"}); err != nil {
		t.Fatalf("enqueue suspend: %v", err)
	}

	if _, err := enqueueBoxReaperOperation(ctx, pool, projectID, envID, boxName,
		models.ActionDeleteBox, "reaper", models.DeleteBoxPayload{BoxID: boxID, Reason: "reaper"}); err != nil {
		t.Fatalf("enqueue delete while suspend is pending: %v", err)
	}

	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionSuspendBox); n != 1 {
		t.Fatalf("suspend rows = %d, want 1", n)
	}
	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionDeleteBox); n != 1 {
		t.Fatalf("delete rows = %d, want 1", n)
	}
}
