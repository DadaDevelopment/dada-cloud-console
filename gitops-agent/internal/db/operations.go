package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Operation mirrors the columns the agent needs from the operations table.
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

const claimBatchSize = 10

// ClaimPending atomically claims up to claimBatchSize Created operations,
// marking them Processing, and returns them. Uses SKIP LOCKED so multiple
// replicas can run without contention.
func ClaimPending(ctx context.Context, pool *pgxpool.Pool) ([]Operation, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// FOR UPDATE cannot be used with LEFT JOIN (nullable side); use EXISTS for k8s filter.
	rows, err := tx.Query(ctx, `
		UPDATE operations
		SET    status = 'Processing', updated_at = NOW()
		WHERE  id IN (
			SELECT o.id FROM operations o
			WHERE  o.status = 'Created'
			  AND  o.action NOT IN ('CreateAppServer', 'DeleteAppServer')
			  AND  (
			    o.environment_id IS NULL
			    OR EXISTS (
			        SELECT 1 FROM environments e
			        WHERE e.id = o.environment_id AND e.runtime = 'k8s'
			    )
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
		return nil, fmt.Errorf("commit claim tx: %w", err)
	}
	return ops, nil
}

// MarkCommitted sets status=Committed and records the git commit SHA and path.
func MarkCommitted(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, sha, gitPath string) error {
	_, err := pool.Exec(ctx, `
		UPDATE operations
		SET    status = 'Committed', git_commit = $2, git_path = $3, updated_at = NOW()
		WHERE  id = $1
	`, id, sha, gitPath)
	return err
}

// MarkFailed sets status=Failed with an error message.
func MarkFailed(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, code, message string) error {
	_, err := pool.Exec(ctx, `
		UPDATE operations
		SET    status = 'Failed', error_code = $2, error_message = $3, updated_at = NOW()
		WHERE  id = $1
	`, id, code, message)
	return err
}
