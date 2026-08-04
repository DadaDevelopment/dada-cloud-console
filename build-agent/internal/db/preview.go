package db

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// PreviewEnv is the ephemeral (PR) environment row needed to build the
// teardown operation payload for gitops-agent.
//
// Everything that CREATED such a row is gone: previews are no longer a product
// feature (see handlePullRequestWebhook). What survives here is the teardown
// half, because environments opened before the removal still exist and closing
// their PR must still take them down.
type PreviewEnv struct {
	ID        uuid.UUID
	Name      string
	Namespace string
}

// deletePreviewEnvPayload mirrors backend models.DeletePreviewEnvPayload. JSON
// tags are a hard contract with gitops-agent's doDeletePreviewEnv worker - do
// NOT rename them.
type deletePreviewEnvPayload struct {
	EnvironmentID string `json:"environment_id"`
	Namespace     string `json:"namespace"`
}

// InsertDeletePreviewEnvOp enqueues the DeletePreviewEnv operation that tears
// down a PR's preview environment (git-rendered manifests, namespace, and the
// environments row itself). actor is SystemUserID for a webhook-driven PR
// close event.
func InsertDeletePreviewEnvOp(ctx context.Context, pool *pgxpool.Pool, actor, projectID, envID uuid.UUID, namespace string) (uuid.UUID, error) {
	payload, err := json.Marshal(deletePreviewEnvPayload{
		EnvironmentID: envID.String(),
		Namespace:     namespace,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal DeletePreviewEnv payload: %w", err)
	}
	var opID uuid.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		VALUES ($1, $2, $3, 'DeletePreviewEnv', 'Environment', $4, 'Created', $5)
		RETURNING id
	`, actor, projectID, envID, namespace, payload).Scan(&opID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert DeletePreviewEnv operation: %w", err)
	}
	recordPreviewAudit(ctx, pool, actor, projectID, envID, opID, "DeletePreviewEnv", namespace, map[string]any{
		"trigger": "pr_event",
	})
	return opID, nil
}

// recordPreviewAudit writes the audit row for a preview environment's death.
// The operation is enqueued from a GitHub webhook, so the actor is the system
// user, but the row is still the only place the event is legible to path
// analysis: closing a PR is something a person did, and without these rows the
// whole preview feature was absent from the funnel (on prod, 17
// CreatePreviewEnv operations in 30 days against zero audit rows).
//
// Best-effort by contract, like the deploy and build-notify audit rows: a
// bookkeeping failure must never break the webhook path.
func recordPreviewAudit(ctx context.Context, pool *pgxpool.Pool, actor, projectID, envID, opID uuid.UUID, action, namespace string, meta map[string]any) {
	if pool == nil {
		return
	}
	payload, err := json.Marshal(meta)
	if err != nil {
		payload = []byte("{}")
	}
	var env any
	if envID != uuid.Nil {
		env = envID
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_events (actor_id, project_id, environment_id, operation_id, action, resource_kind, resource_name, metadata)
		VALUES ($1, $2, $3, $4, $5, 'Environment', $6, $7)
	`, actor, projectID, env, opID, action, namespace, payload); err != nil {
		log.Warn().Err(err).Str("namespace", namespace).Str("action", action).Msg("preview: audit row insert failed")
	}
}

// EnvPreviewInfo returns whether an environment is ephemeral and, if so, the PR
// head branch it tracks. Used by HandoffDeploy's CreateApp branch to decide
// between the normal default-domain hostname and a per-branch preview
// hostname, which legacy preview environments still need while they exist.
func EnvPreviewInfo(ctx context.Context, q RowQuerier, envID uuid.UUID) (isEphemeral bool, headBranch string, err error) {
	var branch *string
	err = q.QueryRow(ctx, `
		SELECT is_ephemeral, pr_head_branch FROM environments WHERE id = $1
	`, envID).Scan(&isEphemeral, &branch)
	if err != nil {
		return false, "", fmt.Errorf("env preview info: %w", err)
	}
	if branch != nil {
		headBranch = *branch
	}
	return isEphemeral, headBranch, nil
}

// FindPreviewEnvByPR looks up the ephemeral environment for a PR, used by the
// pull_request "closed" handler to find what to tear down. Returns (nil, nil)
// when no preview environment exists for this (repo, PR) - which is now the
// normal case, since no new preview environment is ever created.
func FindPreviewEnvByPR(ctx context.Context, pool *pgxpool.Pool, gitRepoID uuid.UUID, prNumber int) (*PreviewEnv, error) {
	var pe PreviewEnv
	err := pool.QueryRow(ctx, `
		SELECT id, name, namespace FROM environments
		WHERE git_repo_id = $1 AND pr_number = $2 AND is_ephemeral
	`, gitRepoID, prNumber).Scan(&pe.ID, &pe.Name, &pe.Namespace)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find preview env by pr: %w", err)
	}
	return &pe, nil
}
