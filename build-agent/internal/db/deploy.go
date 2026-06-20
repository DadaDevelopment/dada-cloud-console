package db

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SystemUserID is the fixed, non-loginable actor used for agent-initiated
// operations (010_system_user.sql). build-agent enqueues DeployImageVersion as
// this actor.
var SystemUserID = uuid.MustParse("00000000-0000-0000-0000-000000000000")

// deployImageVersionPayload mirrors backend models.DeployImageVersionPayload.
// Kept local so build-agent does not import the backend module.
type deployImageVersionPayload struct {
	AppName string `json:"app_name"`
	Image   string `json:"image"`
}

// HandoffDeploy is the success-path deploy handoff (plan §4, invariant 2). It is
// the ONLY way build-agent re-enters the declarative path: it writes a
// deployments row + a DeployImageVersion operation, then links them. It NEVER
// touches Argo/Helm/k8s workloads — the existing gitops rails take it from here.
//
// Steps (single tx so a crash never leaves a dangling deployment):
//  1. INSERT deployments (not yet current — the op-Ready watcher flips is_current).
//  2. INSERT operations (DeployImageVersion, status=Created, actor=system).
//  3. UPDATE deployments.operation_id = <op id>.
//
// Returns the new operation id.
func HandoffDeploy(ctx context.Context, pool *pgxpool.Pool, b *Build, projectID uuid.UUID, imageURI string) (uuid.UUID, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin deploy tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	deployTrigger := b.Trigger
	switch deployTrigger {
	case "push", "pr", "manual", "rollback", "promote":
	default:
		deployTrigger = "push"
	}

	var deployID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO deployments (environment_id, app_name, build_id, image_uri, trigger, deployed_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, b.EnvironmentID, b.AppName, b.ID, imageURI, deployTrigger, SystemUserID).Scan(&deployID); err != nil {
		return uuid.Nil, fmt.Errorf("insert deployment: %w", err)
	}

	payload, err := json.Marshal(deployImageVersionPayload{AppName: b.AppName, Image: imageURI})
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal deploy payload: %w", err)
	}

	var opID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		VALUES ($1, $2, $3, 'DeployImageVersion', 'App', $4, 'Created', $5)
		RETURNING id
	`, SystemUserID, projectID, b.EnvironmentID, b.AppName, payload).Scan(&opID); err != nil {
		return uuid.Nil, fmt.Errorf("insert operation: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE deployments SET operation_id = $1 WHERE id = $2`, opID, deployID); err != nil {
		return uuid.Nil, fmt.Errorf("link operation: %w", err)
	}

	// Best-effort audit (matches backend deployments.go).
	auditMeta, _ := json.Marshal(deployImageVersionPayload{AppName: b.AppName, Image: imageURI})
	_, _ = tx.Exec(ctx, `
		INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		VALUES ($1, $2, $3, 'DeployImageVersion', 'App', $4, $5)
	`, SystemUserID, projectID, opID, b.AppName, auditMeta)

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit deploy: %w", err)
	}
	return opID, nil
}

// LatestImageForBranch returns the image_uri of the most recent successful build
// on a repo+branch, if any. Used by the cache-warm/cache-ref decision; harmless
// when absent.
func LatestImageForBranch(ctx context.Context, pool *pgxpool.Pool, gitRepoID uuid.UUID, branch string) (string, error) {
	var uri string
	err := pool.QueryRow(ctx, `
		SELECT image_uri FROM builds
		WHERE  git_repo_id = $1 AND branch = $2 AND status = 'success' AND image_uri IS NOT NULL
		ORDER  BY created_at DESC
		LIMIT  1
	`, gitRepoID, branch).Scan(&uri)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("latest image: %w", err)
	}
	return uri, nil
}
