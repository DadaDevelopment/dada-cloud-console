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

// EnqueueDeletePreviewEnvsForApp enqueues a DeletePreviewEnv operation for every
// environment whose git_repo_id points at the given app's git_repos row(s),
// scoped by project/environment/app_name the same way doDeleteApp's own
// cleanup queries are scoped. Called from inside doDeleteApp, right before it
// detaches and deletes that git_repos row: environments.git_repo_id is
// ON DELETE CASCADE (backend/migrations/014_preview_environments.sql), so
// deleting the row while a preview environment still references it would
// silently delete that environment row and leak its namespace. Enqueuing a
// real teardown first, then nulling the reference (see doDeleteApp), lets the
// git_repos row be deleted unconditionally without ever leaking a namespace or
// leaving a phantom NotDeployed placeholder behind.
//
// Attribution and de-duplication mirror ReapExpiredPreviewEnvs: systemActorID,
// because the user asked to delete an app, not this environment, and a
// NOT EXISTS guard so a retried DeleteApp operation cannot double-enqueue a
// teardown that is already Created or Processing. The payload shape
// (environment_id, namespace) matches what doDeletePreviewEnv unmarshals in
// gitops-agent/internal/worker/preview.go. Returns the ids of the environments
// a teardown was enqueued for, so the caller can null out their git_repo_id.
func EnqueueDeletePreviewEnvsForApp(ctx context.Context, pool *pgxpool.Pool, projectID, environmentID uuid.UUID, appName string) ([]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `
		INSERT INTO operations
			(actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		SELECT
			$1::uuid, e.project_id, e.id, 'DeletePreviewEnv', 'Environment', e.namespace, 'Created',
			jsonb_build_object('environment_id', e.id::text, 'namespace', e.namespace)
		FROM environments e
		JOIN git_repos gr ON gr.id = e.git_repo_id
		WHERE gr.project_id = $2 AND gr.environment_id = $3 AND gr.app_name = $4
		  AND NOT EXISTS (
			SELECT 1 FROM operations o
			WHERE o.environment_id = e.id
			  AND o.action = 'DeletePreviewEnv'
			  AND o.status IN ('Created', 'Processing')
		  )
		RETURNING environment_id
	`, systemActorID, projectID, environmentID, appName)
	if err != nil {
		return nil, fmt.Errorf("enqueue preview teardown for app %s: %w", appName, err)
	}
	defer rows.Close()

	var envIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan enqueued preview teardown env id: %w", err)
		}
		envIDs = append(envIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return envIDs, nil
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
