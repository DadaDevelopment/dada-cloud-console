package db

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Operation mirrors the columns the agent needs from the operations table.
type Operation struct {
	ID            uuid.UUID
	ActorID       uuid.UUID
	ProjectID     uuid.UUID
	EnvironmentID *uuid.UUID
	Action        string
	ResourceKind  string
	ResourceName  string
	Payload       json.RawMessage
	CreatedAt     time.Time
}

// SystemActorID is the fixed-UUID non-loginable user (migration 010) the
// platform files its own operations under: deploy hooks, the app autoscaler,
// preview reaping. An operation carrying it has no human watching its result,
// which is why the render-clobber guard applies to it and not to a deploy a
// person clicked.
var SystemActorID = uuid.MustParse(systemActorID)

// Unattended reports whether the platform, rather than a person, asked for this
// operation.
func (o Operation) Unattended() bool { return o.ActorID == SystemActorID }

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
		RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name, payload, created_at
	`, claimBatchSize)
	if err != nil {
		return nil, fmt.Errorf("claim query: %w", err)
	}
	defer rows.Close()

	var ops []Operation
	for rows.Next() {
		var op Operation
		if err := rows.Scan(
			&op.ID, &op.ActorID, &op.ProjectID, &op.EnvironmentID,
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

// MarkFailed sets status=Failed with an error message, and records the failure
// in audit_events.
func MarkFailed(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, code, message string) error {
	_, err := pool.Exec(ctx, `
		UPDATE operations
		SET    status = 'Failed', error_code = $2, error_message = $3, updated_at = NOW()
		WHERE  id = $1
	`, id, code, message)
	if err != nil {
		return err
	}
	recordFailureAudit(ctx, pool, id, code, message)
	return nil
}

// recordFailureAudit writes an outcome=failure audit row for an operation that
// died inside the worker. Every audit row an API handler writes is recorded at
// enqueue time, when nothing is known yet except that the user asked -- so an
// action that is accepted and then fails asynchronously stays outcome=success in
// audit_events forever, and path analysis counts a failed deploy, a failed
// database, a failed move as things that worked. This is the only terminal
// failure path for operations the gitops agent runs [dbwatcher.go poll].
//
// The row is built from the operations row itself so it cannot disagree with it
// about actor, project, environment or resource, and it carries the same action
// as the success row: the pair reads as intent then result, distinguished by
// outcome. The NOT EXISTS guard keeps a retried MarkFailed from stacking rows.
//
// Best-effort: the operation is already marked Failed and must not be resurrected
// by a bookkeeping error.
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
		log.Printf("mark failed: audit row insert failed for op %s: %v", id, err)
	}
}

// EnqueueDeployStack creates a follow-up DeployStack operation for a compose
// app, copying actor/project/environment from the parent (render) operation.
// The portainer-agent claims and executes it (CreateStackFromGit / RedeployStack).
func EnqueueDeployStack(ctx context.Context, pool *pgxpool.Pool, parentOpID uuid.UUID, appName string, volumes []string) (uuid.UUID, error) {
	if volumes == nil {
		volumes = []string{}
	}
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		SELECT actor_id, project_id, environment_id, 'DeployStack', 'App', $2::text, 'Created',
		       jsonb_build_object('app_name', $2::text, 'volumes', $3::jsonb)
		FROM operations WHERE id = $1
		RETURNING id`,
		parentOpID, appName, volumes,
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
