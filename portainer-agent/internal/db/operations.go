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

// ClaimPending atomically claims up to claimBatchSize Created operations for the VM track.
// VM-track operations are:
//   - action IN ('CreateAppServer', 'DeleteAppServer') — no environment
//   - environment.runtime = 'vm' — all other actions on VM environments
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
			LEFT JOIN environments e ON e.id = o.environment_id
			WHERE  o.status = 'Created'
			  AND  (
			    o.action IN ('CreateAppServer', 'DeleteAppServer')
			    OR e.runtime = 'vm'
			  )
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
