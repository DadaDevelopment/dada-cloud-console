package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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

// MarkFailed sets status=Failed with an error code and message.
func MarkFailed(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, code, message string) error {
	_, err := pool.Exec(ctx,
		`UPDATE operations SET status = 'Failed', error_code = $2, error_message = $3, updated_at = NOW() WHERE id = $1`,
		id, code, message,
	)
	return err
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
