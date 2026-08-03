package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// Operation mirrors the columns portainer-agent needs from the operations table.
type Operation struct {
	ID            uuid.UUID
	ProjectID     uuid.UUID
	EnvironmentID *uuid.UUID
	Action        string
	ResourceKind  string
	ResourceName  string
	Payload       json.RawMessage
	CreatedAt     time.Time
}

const claimBatchSize = 5

// ClaimPending atomically claims up to claimBatchSize Created operations the
// portainer-agent owns. Ownership is by action only (disjoint from the
// gitops-agent's claim set):
//   - CreateAppServer / DeleteAppServer — VM lifecycle (no environment)
//   - DeployStack — deploy/redeploy a compose stack onto an endpoint
//   - DiscoverWorkload — read-only inventory of an endpoint's containers/volumes
func ClaimPending(ctx context.Context, pool *pgxpool.Pool) ([]Operation, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	rows, err := tx.Query(ctx, `
		UPDATE operations
		SET    status = 'Processing', updated_at = NOW()
		WHERE  id IN (
			SELECT o.id FROM operations o
			WHERE  o.status = 'Created'
			  AND  o.action IN ('CreateAppServer', 'DeleteAppServer', 'DeployStack', 'DiscoverWorkload', 'RestartStack')
			ORDER  BY o.created_at
			LIMIT  $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, project_id, environment_id, action, resource_kind, resource_name, payload, created_at
	`, claimBatchSize)
	if err != nil {
		return nil, fmt.Errorf("claim query: %w", err)
	}
	defer rows.Close()

	var ops []Operation
	for rows.Next() {
		var op Operation
		if err := rows.Scan(
			&op.ID, &op.ProjectID, &op.EnvironmentID,
			&op.Action, &op.ResourceKind, &op.ResourceName,
			&op.Payload, &op.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning operation: %w", err)
		}
		ops = append(ops, op)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return ops, nil
}

// UpdateStatus sets the operation status (clears error fields).
func UpdateStatus(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, status string) error {
	_, err := pool.Exec(ctx,
		`UPDATE operations SET status = $2, error_code = NULL, error_message = NULL, updated_at = NOW() WHERE id = $1`,
		id, status,
	)
	return err
}

// MarkFailed sets status=Failed with an error code and message, and records the
// failure in audit_events.
func MarkFailed(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, code, message string) error {
	_, err := pool.Exec(ctx,
		`UPDATE operations SET status = 'Failed', error_code = $2, error_message = $3, updated_at = NOW() WHERE id = $1`,
		id, code, message,
	)
	if err != nil {
		return err
	}
	recordFailureAudit(ctx, pool, id, code, message)
	return nil
}

// recordFailureAudit writes an outcome=failure audit row for an operation that
// died inside this agent. The handler's audit row is written at enqueue time,
// when the only thing known is that the user asked, so an action that is
// accepted and then fails asynchronously otherwise stays outcome=success in
// audit_events forever -- a failed AppServer enrolment reads exactly like one
// that worked. portainer-agent owns CreateAppServer, DeleteAppServer,
// DeployStack, DiscoverWorkload and RestartStack: the gitops agent's claim query
// excludes all five [gitops-agent/internal/db/operations.go ClaimPending].
//
// The row is built from the operations row itself so it cannot disagree with it
// about actor, project, environment or resource, and carries the same action as
// the success row: the pair reads as intent then result, told apart by outcome.
// The NOT EXISTS guard keeps a retried MarkFailed from stacking rows.
//
// Best-effort: the operation is already Failed and must not be resurrected by a
// bookkeeping error.
func recordFailureAudit(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, code, message string) {
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_events
			(actor_id, project_id, environment_id, operation_id, action, resource_kind, resource_name, outcome, metadata)
		SELECT o.actor_id, o.project_id, o.environment_id, o.id, o.action, o.resource_kind, o.resource_name, 'failure',
		       jsonb_build_object('reason', $2::text, 'error', left($3::text, 300), 'phase', 'operation')
		  FROM operations o
		 WHERE o.id = $1
		   AND NOT EXISTS (
			SELECT 1 FROM audit_events a
			 WHERE a.operation_id = o.id AND a.outcome = 'failure'
		   )
	`, id, code, message); err != nil {
		log.Warn().Err(err).Str("operation", id.String()).Msg("mark failed: audit row insert failed")
	}
}

// MarkReady sets status=Ready.
func MarkReady(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) error {
	return UpdateStatus(ctx, pool, id, "Ready")
}

// SaveValidationResult stores a handler's JSON result in operations.validation_result
// (jsonb). Used by read-only ops like DiscoverWorkload to hand a payload back to
// the API/console without a bespoke result table.
func SaveValidationResult(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, result json.RawMessage) error {
	_, err := pool.Exec(ctx,
		`UPDATE operations SET validation_result = $2, updated_at = NOW() WHERE id = $1`,
		id, result,
	)
	return err
}

// ScrubOperationSecret removes one-shot secret fields from an operation's JSONB
// payload. Called once the operation reaches a terminal state so SSH private
// keys for manual VM connect are never retained at rest.
func ScrubOperationSecret(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	// jsonb "- text[]" removes each named key from the top-level object.
	_, err := pool.Exec(ctx,
		`UPDATE operations SET payload = payload - $2::text[], updated_at = NOW() WHERE id = $1`,
		id, keys,
	)
	return err
}
