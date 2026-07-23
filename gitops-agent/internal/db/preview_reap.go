package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReapExpiredPreviewEnvs enqueues a DeletePreviewEnv operation for every
// ephemeral environment whose TTL has expired and that has no teardown
// already Created or Processing for it. It is the TTL reaper's only query:
// the SELECT and INSERT run as one statement so a concurrent reaper tick (or a
// webhook-driven "closed" teardown racing the same environment) can never
// double-enqueue — the NOT EXISTS guard is evaluated atomically with the
// insert inside Postgres, and the environment's own unique row can only
// satisfy it once per outstanding operation.
//
// The operation is attributed to systemActorID (mirrors
// EnqueueDeployStackBySlug), since expiry has no originating user request.
// The payload shape (environment_id, namespace) matches what
// doDeletePreviewEnv unmarshals in gitops-agent/internal/worker/preview.go.
func ReapExpiredPreviewEnvs(ctx context.Context, pool *pgxpool.Pool) ([]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `
		INSERT INTO operations
			(actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		SELECT
			$1::uuid, e.project_id, e.id, 'DeletePreviewEnv', 'Environment', e.namespace, 'Created',
			jsonb_build_object('environment_id', e.id::text, 'namespace', e.namespace)
		FROM environments e
		WHERE e.is_ephemeral
		  AND e.expires_at < NOW()
		  AND NOT EXISTS (
			SELECT 1 FROM operations o
			WHERE o.environment_id = e.id
			  AND o.action = 'DeletePreviewEnv'
			  AND o.status IN ('Created', 'Processing')
		  )
		RETURNING id
	`, systemActorID)
	if err != nil {
		return nil, fmt.Errorf("reap expired preview envs: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan enqueued reap op: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
