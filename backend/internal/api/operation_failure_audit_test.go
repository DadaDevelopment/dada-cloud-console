package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestRecordOperationFailureAudit pins the failure row a background worker owes
// an operation it gave up on. The box worker suspends and resumes boxes without
// a user watching, and 256 SuspendBox operations had failed on prod against zero
// failure rows in audit_events [live psql, 60d] — every one of them still reads
// as a success.
func TestRecordOperationFailureAudit(t *testing.T) {
	pool := testOptimisticPool(t)
	ctx := context.Background()
	userID := seedUser(t, pool)

	var projectID uuid.UUID
	name := "p-" + uuid.NewString()[:8]
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`, name,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, projectID) })

	var opID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO operations (actor_id, project_id, action, resource_kind, resource_name, status, payload)
		VALUES ($1, $2, 'SuspendBox', 'Box', 'demo-box', 'Failed', '{}'::jsonb)
		RETURNING id`, userID, projectID).Scan(&opID); err != nil {
		t.Fatalf("seed operation: %v", err)
	}

	recordOperationFailureAudit(ctx, pool, opID, "max_attempts", "box agent unreachable")

	var action, kind, name2, reason, errText string
	var actor uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT action, resource_kind, resource_name, actor_id, metadata->>'reason', metadata->>'error'
		  FROM audit_events WHERE operation_id = $1 AND outcome = 'failure'`, opID,
	).Scan(&action, &kind, &name2, &actor, &reason, &errText); err != nil {
		t.Fatalf("a worker gave up on an operation but wrote no audit row — the action stays a success in path analysis: %v", err)
	}
	if action != "SuspendBox" || kind != "Box" || name2 != "demo-box" {
		t.Errorf("row = %s %s/%s, want SuspendBox Box/demo-box", action, kind, name2)
	}
	if actor != userID {
		t.Errorf("actor_id = %s, want the operation's own actor %s", actor, userID)
	}
	if reason != "max_attempts" || errText != "box agent unreachable" {
		t.Errorf("metadata = reason %q / error %q, want max_attempts / box agent unreachable", reason, errText)
	}

	recordOperationFailureAudit(ctx, pool, opID, "max_attempts", "box agent unreachable")
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events WHERE operation_id = $1 AND outcome = 'failure'`, opID).Scan(&n); err != nil {
		t.Fatalf("count failure rows: %v", err)
	}
	if n != 1 {
		t.Errorf("failure rows = %d, want 1 — a retried terminal write must not stack rows", n)
	}
}
