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

	// gitops-agent owns git rendering for ALL runtimes (k8s Helm apps and VM
	// compose apps alike). The portainer-agent owns VM/endpoint lifecycle,
	// stack deploys, and read-only workload discovery, so those actions are
	// excluded here. The split is purely by action, making the two claim sets
	// disjoint regardless of env.runtime — this list MUST mirror the exclusion of
	// portainer-agent's ClaimPending include list (add new VM actions to both).
	//
	// THIS IS A DENYLIST, AND THAT MAKES IT A LANDMINE FOR EVERY NEW ACTION.
	// Anything not named here is claimed by this agent, and anything it claims
	// that its dispatch switch does not know is failed immediately with
	// "unknown action". So an action owned by a *third* agent must be excluded
	// here on the same commit that introduces it, or the feature is dead on
	// arrival with a confusing error and no retry. portainer-agent, by contrast,
	// uses an allowlist and needs no edit for a foreign action.
	//
	// The ten Box* actions below are owned by box-agent (a separate module, not
	// yet written; see docs/plans/2026-07-29-box-runtime-architecture.md). They
	// are excluded ahead of that agent existing on purpose: until it ships, a box
	// operation sits in Created — visibly pending — instead of being claimed here
	// and marked Failed. Keep this list byte-identical to models.BoxActions in the
	// backend module (the two cannot import each other).
	rows, err := tx.Query(ctx, `
		UPDATE operations
		SET    status = 'Processing', updated_at = NOW()
		WHERE  id IN (
			SELECT o.id FROM operations o
			WHERE  o.status = 'Created'
			  AND  o.action NOT IN ('CreateAppServer', 'DeleteAppServer', 'DeployStack', 'DiscoverWorkload', 'RestartStack',
			                        'BoxUp', 'SuspendBox', 'ResumeBox', 'DeleteBox',
			                        'AttachBoxDatabase', 'AttachBoxS3', 'DetachBoxAttachment',
			                        'ExposeBox', 'UnexposeBox', 'CrystallizeBox')
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

// EnqueueDeployStack creates a follow-up DeployStack operation for a compose
// app, copying actor/project/environment from the parent (render) operation.
// The portainer-agent claims and executes it (CreateStackFromGit / RedeployStack).
func EnqueueDeployStack(ctx context.Context, pool *pgxpool.Pool, parentOpID uuid.UUID, appName string) (uuid.UUID, error) {
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		SELECT actor_id, project_id, environment_id, 'DeployStack', 'App', $2::text, 'Created',
		       jsonb_build_object('app_name', $2::text)
		FROM operations WHERE id = $1
		RETURNING id`,
		parentOpID, appName,
	).Scan(&id)
	return id, err
}

// systemActorID is the fixed-UUID non-loginable user (migration 010) used as the
// actor for agent-initiated operations that have no originating user request.
const systemActorID = "00000000-0000-0000-0000-000000000000"

// EnqueueDeployStackBySlug creates a DeployStack operation for a compose app
// identified by project/env slugs, attributed to the system actor. Used by the
// editor save path (which has no originating user/operation). Returns
// pgx.ErrNoRows if the project/env slugs don't resolve.
func EnqueueDeployStackBySlug(ctx context.Context, pool *pgxpool.Pool, projectSlug, envSlug, appName string) (uuid.UUID, error) {
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		SELECT $4::uuid, p.id, e.id, 'DeployStack', 'App', $3::text, 'Created',
		       jsonb_build_object('app_name', $3::text)
		FROM projects p JOIN environments e ON e.project_id = p.id
		WHERE p.name = $1 AND e.name = $2
		RETURNING id`,
		projectSlug, envSlug, appName, systemActorID,
	).Scan(&id)
	return id, err
}
