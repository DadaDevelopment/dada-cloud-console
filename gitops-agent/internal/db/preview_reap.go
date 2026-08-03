package db

import (
	"context"
	"fmt"
	"log"

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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	recordReapAudit(ctx, pool, ids)
	return ids, nil
}

// recordReapAudit writes one audit row per teardown the reaper just enqueued.
// A preview environment expiring is the one end-of-life a user never asks for,
// so without this row the environment simply stops existing and nothing says
// why -- the same blank as a preview that was never created. The row is built
// from the operations rows themselves so it cannot disagree with them about
// project, environment or namespace.
//
// Best-effort: the teardown is already enqueued and must not be undone by a
// bookkeeping failure. The audit row survives the environment's deletion
// because migration 093 made audit_events.environment_id ON DELETE SET NULL.
func recordReapAudit(ctx context.Context, pool *pgxpool.Pool, ids []uuid.UUID) {
	if len(ids) == 0 {
		return
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_events
			(actor_id, project_id, environment_id, operation_id, action, resource_kind, resource_name, metadata)
		SELECT o.actor_id, o.project_id, o.environment_id, o.id, o.action, 'Environment', o.resource_name,
		       jsonb_build_object('trigger', 'ttl_expired')
		  FROM operations o
		 WHERE o.id = ANY($1)
	`, ids); err != nil {
		log.Printf("reap preview envs: audit rows insert failed: %v", err)
	}
}
