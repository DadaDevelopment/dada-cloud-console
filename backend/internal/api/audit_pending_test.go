package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// An audit row written while its operation is still running claims a result
// nobody has yet.
//
// Every handler audits at enqueue time: the transaction inserting the operation
// with status Created commits, the audit row follows with no outcome set, and
// nothing ever updates it because audit_events is append-only. So an action that
// was merely accepted -- and then failed to render, failed to reach git, or was
// never picked up at all -- stays outcome=success forever, and activation
// measured off outcome='success' counts clicks instead of deploys.
//
// The three cases below are the whole contract: unfinished operation downgrades
// to pending, finished operation keeps the caller's word, and a row with no
// operation behind it is unaffected.
func TestWriteAuditRow_UnfinishedOperationIsPending(t *testing.T) {
	pool := testOptimisticPool(t)
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)
	opID := seedAuditOperation(t, pool, userID, projectID, envID, "Created")

	writeAuditRow(context.Background(), pool, userID, auditEntry{
		Action:        "CreateApp",
		ResourceKind:  "App",
		ResourceName:  "shop",
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   opID,
	})

	if got := auditOutcomeOf(t, pool, opID); got != auditOutcomePending {
		t.Errorf("outcome = %q, want %q — an operation still in Created has produced no result to report", got, auditOutcomePending)
	}
}

func TestWriteAuditRow_FinishedOperationKeepsSuccess(t *testing.T) {
	pool := testOptimisticPool(t)
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)
	opID := seedAuditOperation(t, pool, userID, projectID, envID, "Committed")

	writeAuditRow(context.Background(), pool, userID, auditEntry{
		Action:        "CreateApp",
		ResourceKind:  "App",
		ResourceName:  "shop",
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   opID,
	})

	if got := auditOutcomeOf(t, pool, opID); got != auditOutcomeSuccess {
		t.Errorf("outcome = %q, want %q — a terminal writer must keep its own verdict", got, auditOutcomeSuccess)
	}
}

// A caller that reports a failure knows more than the operation's status does,
// and a synchronous action has no operation to wait for. Neither may be
// rewritten, or the downgrade would erase results instead of deferring them.
func TestWriteAuditRow_FailureAndOperationlessRowsUntouched(t *testing.T) {
	pool := testOptimisticPool(t)
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)
	opID := seedAuditOperation(t, pool, userID, projectID, envID, "Processing")

	writeAuditRow(context.Background(), pool, userID, auditEntry{
		Action:        "CreateApp",
		ResourceKind:  "App",
		ResourceName:  "shop",
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   opID,
		Outcome:       auditOutcomeFailure,
	})
	if got := auditOutcomeOf(t, pool, opID); got != auditOutcomeFailure {
		t.Errorf("outcome = %q, want %q — a handler that knows it failed must not be softened to pending", got, auditOutcomeFailure)
	}

	writeAuditRow(context.Background(), pool, userID, auditEntry{
		Action:       auditActionRevealEnvVar,
		ResourceKind: "EnvVar",
		ResourceName: "DATABASE_URL",
		ProjectID:    projectID,
	})
	var outcome string
	if err := pool.QueryRow(context.Background(),
		`SELECT outcome FROM audit_events WHERE project_id = $1 AND action = $2`,
		projectID, auditActionRevealEnvVar).Scan(&outcome); err != nil {
		t.Fatalf("read operationless row: %v", err)
	}
	if outcome != auditOutcomeSuccess {
		t.Errorf("outcome = %q, want %q — a synchronous action has already happened when its row is written", outcome, auditOutcomeSuccess)
	}
}

// seedAuditOperation inserts one operation in the given status for an audit row
// to hang off.
func seedAuditOperation(t *testing.T, pool *pgxpool.Pool, actorID, projectID, envID uuid.UUID, status string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		VALUES ($1, $2, $3, 'CreateApp', 'App', 'shop', $4, '{}'::jsonb)
		RETURNING id`, actorID, projectID, envID, status).Scan(&id); err != nil {
		t.Fatalf("seed operation: %v", err)
	}
	return id
}

func auditOutcomeOf(t *testing.T, pool *pgxpool.Pool, opID uuid.UUID) string {
	t.Helper()
	var outcome string
	if err := pool.QueryRow(context.Background(),
		`SELECT outcome FROM audit_events WHERE operation_id = $1`, opID).Scan(&outcome); err != nil {
		t.Fatalf("read audit row for operation %s: %v", opID, err)
	}
	return outcome
}
