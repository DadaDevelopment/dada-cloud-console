package api

import (
	"context"
	"testing"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedBoxAuditProject makes a project and environment for the platform-initiated
// box operations to hang off, and removes them again afterwards.
func seedBoxAuditProject(t *testing.T, pool *pgxpool.Pool) (context.Context, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"box-audit-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, projectID) })

	var envID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO environments (project_id, name, namespace, type)
		VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "box-audit-"+suffix+"-prod",
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	return ctx, projectID, envID
}

// TestEnqueueBoxReaperOperation_AuditsUnderTheOperationID pins the fix for the
// biggest hole the coverage report found: 271 of 274 SuspendBox operations in
// 30 days had no audit row, because the reaper and the meter enqueued straight
// into operations while every user-facing box verb audited its own enqueue.
//
// The assertion is on the LINK, not on the row's existence. An audit row that
// names the action but not the operation is invisible to the coverage join and
// therefore still reads as a box the platform killed silently.
func TestEnqueueBoxReaperOperation_AuditsUnderTheOperationID(t *testing.T) {
	pool := testOptimisticPool(t)
	ctx, projectID, envID := seedBoxAuditProject(t, pool)

	boxID := uuid.New()
	opID, err := enqueueBoxReaperOperation(ctx, pool, projectID, envID, "reaped-box",
		models.ActionSuspendBox, "idle_ttl",
		models.SuspendBoxPayload{BoxID: boxID, Reason: "idle_ttl"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if opID == uuid.Nil {
		t.Fatal("enqueue returned a nil operation id; the audit row cannot be joined to anything")
	}

	var action, kind, name, outcome, trigger, reason string
	if err := pool.QueryRow(ctx, `
		SELECT action, resource_kind, resource_name, outcome,
		       metadata->>'trigger', metadata->>'reason'
		  FROM audit_events WHERE operation_id = $1`, opID,
	).Scan(&action, &kind, &name, &outcome, &trigger, &reason); err != nil {
		t.Fatalf("no audit row joined to operation %s: %v", opID, err)
	}
	if action != models.ActionSuspendBox {
		t.Errorf("action = %q, want %q", action, models.ActionSuspendBox)
	}
	if kind != models.ResourceKindBox || name != "reaped-box" {
		t.Errorf("resource = %s/%s, want %s/reaped-box", kind, name, models.ResourceKindBox)
	}
	if outcome != auditOutcomePending {
		t.Errorf("outcome = %q, want %q — the reaper has enqueued a suspend, not performed one", outcome, auditOutcomePending)
	}
	if trigger != "platform" || reason != "idle_ttl" {
		t.Errorf("metadata trigger/reason = %s/%s, want platform/idle_ttl", trigger, reason)
	}
}

// TestEnqueueBoxReaperOperation_ClosesTheCoverageGap runs the shipped report over
// the freshly enqueued operation. The previous test proves a row exists with the
// right shape; this one proves the report agrees, which is the only claim the
// dashboard actually makes.
func TestEnqueueBoxReaperOperation_ClosesTheCoverageGap(t *testing.T) {
	pool := testOptimisticPool(t)
	ctx, projectID, envID := seedBoxAuditProject(t, pool)

	action := "DeleteBoxProbe" + uuid.NewString()[:8]
	if _, err := enqueueBoxReaperOperation(ctx, pool, projectID, envID, "doomed-box",
		action, "spend_cap",
		models.DeleteBoxPayload{BoxID: uuid.New(), Reason: "spend_cap"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if gap := findGap(callAuditCoverage(t, pool), action); gap != nil {
		t.Fatalf("coverage still reports %s as a gap: %d operations, %d audited", action, gap.Operations, gap.Audited)
	}
}

// TestInsertProject_ReturnsOperationIDForAudit covers the other half of the same
// defect. CreateProject wrote its audit row from the handler while insertProject
// swallowed the operation id, so prod carried 17 operations and 9 audit rows that
// could not be joined -- the report read a fully audited action as entirely silent.
func TestInsertProject_ReturnsOperationIDForAudit(t *testing.T) {
	pool := testOptimisticPool(t)
	ctx := context.Background()

	h := &Handler{pool: pool}
	slug := "insertproj-" + uuid.NewString()[:8]

	var ownerID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, display_name)
		 VALUES ($1, $2, 'x', $1) RETURNING id`,
		slug, slug+"@example.test",
	).Scan(&ownerID); err != nil {
		t.Fatalf("seed owner: %v", err)
	}

	projectID, envID, opID, err := h.insertProject(ctx, ownerID, slug, slug, "test-org", "prod")
	if err != nil {
		t.Fatalf("insertProject: %v", err)
	}
	t.Cleanup(func() {
		dropSeededProject(pool, projectID)
		dropSeededUser(pool, ownerID)
	})
	if envID == uuid.Nil {
		t.Fatal("insertProject returned a nil environment id")
	}

	var action string
	if err := pool.QueryRow(ctx,
		`SELECT action FROM operations WHERE id = $1 AND project_id = $2`, opID, projectID,
	).Scan(&action); err != nil {
		t.Fatalf("returned operation id %s does not name a CreateProject operation: %v", opID, err)
	}
	if action != "CreateProject" {
		t.Errorf("action = %q, want CreateProject", action)
	}
}
