package db

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PreviewEnv is the ephemeral (PR) environment row needed to target a preview
// build/deploy and to build the operations payload for gitops-agent.
type PreviewEnv struct {
	ID        uuid.UUID
	Name      string
	Namespace string
}

// previewNameUnsafe matches every byte that is not a lowercase DNS-label
// character, used to sanitize the pr-<n>-<app> env name and the project-slug
// derived namespace.
var previewNameUnsafe = regexp.MustCompile(`[^a-z0-9-]+`)

// previewLabel lowercases s and rewrites every non [a-z0-9-] run to a single
// '-', collapsing repeats and trimming leading/trailing '-'.
func previewLabel(s string) string {
	s = strings.ToLower(s)
	s = previewNameUnsafe.ReplaceAllString(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// truncateLabel caps s at max bytes without leaving a trailing '-', so a
// Kubernetes 63-char DNS label limit is respected without changing meaning.
func truncateLabel(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return strings.TrimRight(s[:max], "-")
}

// EnsurePreviewEnv idempotently creates (or refreshes) the ephemeral
// environments row for a PR, mirroring the SQL contract gitops-agent's
// doCreatePreviewEnv (gitops-agent/internal/worker/preview.go:74-107) uses so
// build-agent's synchronous insert and the async CreatePreviewEnv operation it
// enqueues right after can never disagree about the row's shape.
//
// Building the row here (not only via the operation) lets InsertPreviewBuild
// target a real environment_id in the same webhook request:
// builds.environment_id is NOT NULL, so there is no valid intermediate state
// to enqueue-then-build against - the operation only has to (re)render the
// git-side namespace policy and re-copy env_vars, both idempotent.
//
// env name is "pr-<n>-<app>"; namespace is "<project-slug>-pr-<n>-<app>",
// both lowercased and capped at the 63-byte Kubernetes DNS-label limit.
// expiresAt is set to now+ttl; on a repeat call (synchronize) it is bumped the
// same way via ON CONFLICT DO UPDATE.
func EnsurePreviewEnv(ctx context.Context, pool *pgxpool.Pool, projectID, gitRepoID, parentEnvID uuid.UUID, projectSlug, appName string, prNumber int, headBranch string, ttl time.Duration) (PreviewEnv, error) {
	envName := truncateLabel(previewLabel(fmt.Sprintf("pr-%d-%s", prNumber, appName)), 63)
	namespace := truncateLabel(previewLabel(fmt.Sprintf("%s-pr-%d-%s", projectSlug, prNumber, appName)), 63)
	expiresAt := time.Now().Add(ttl)

	var envID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO environments
			(project_id, name, namespace, type, is_ephemeral,
			 git_repo_id, pr_number, pr_head_branch, parent_env_id, expires_at)
		VALUES ($1, $2, $3, 'preview', TRUE, $4, $5, $6, $7, $8)
		ON CONFLICT (project_id, name) DO UPDATE
		SET namespace = EXCLUDED.namespace,
		 is_ephemeral = TRUE,
		 git_repo_id = EXCLUDED.git_repo_id,
		 pr_number = EXCLUDED.pr_number,
		 pr_head_branch = EXCLUDED.pr_head_branch,
		 parent_env_id = EXCLUDED.parent_env_id,
		 expires_at = EXCLUDED.expires_at,
		 updated_at = NOW()
		RETURNING id
	`, projectID, envName, namespace, gitRepoID, prNumber, headBranch, parentEnvID, expiresAt).Scan(&envID)
	if err != nil {
		return PreviewEnv{}, fmt.Errorf("ensure preview env: %w", err)
	}

	if err := copyPreviewEnvVars(ctx, pool, envID, parentEnvID); err != nil {
		return PreviewEnv{}, err
	}

	return PreviewEnv{ID: envID, Name: envName, Namespace: namespace}, nil
}

// copyPreviewEnvVars seeds previewEnvID's env_vars from parentEnvID's env_vars,
// preferring the value/is_secret of any matching row in parentEnvID's
// preview_env_overrides (a key present there wins over the inherited value for
// that same key), then copies override-only keys (no env_vars counterpart on
// the parent) in as ordinary runtime vars. Mirrors gitops-agent's
// copyPreviewEnvVars (gitops-agent/internal/worker/preview.go) byte for byte so
// the synchronous webhook insert and the async idempotent re-run it enqueues
// can never disagree about the preview env's shape.
func copyPreviewEnvVars(ctx context.Context, pool *pgxpool.Pool, previewEnvID, parentEnvID uuid.UUID) error {
	if _, err := pool.Exec(ctx, `
		INSERT INTO env_vars
			(environment_id, app_name, key, value_encrypted, is_secret, scope)
		SELECT $1, e.app_name, e.key,
		       COALESCE(o.value_encrypted, e.value_encrypted),
		       COALESCE(o.is_secret, e.is_secret),
		       e.scope
		FROM env_vars e
		LEFT JOIN preview_env_overrides o
			ON o.environment_id = e.environment_id
			AND o.app_name = e.app_name
			AND o.key = e.key
		WHERE e.environment_id = $2
		ON CONFLICT (environment_id, app_name, key) DO NOTHING
	`, previewEnvID, parentEnvID); err != nil {
		return fmt.Errorf("copy parent env_vars: %w", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO env_vars
			(environment_id, app_name, key, value_encrypted, is_secret, scope)
		SELECT $1, o.app_name, o.key, o.value_encrypted, o.is_secret, 'runtime'
		FROM preview_env_overrides o
		WHERE o.environment_id = $2
		AND NOT EXISTS (
			SELECT 1 FROM env_vars e
			WHERE e.environment_id = o.environment_id
			AND e.app_name = o.app_name
			AND e.key = o.key
		)
		ON CONFLICT (environment_id, app_name, key) DO NOTHING
	`, previewEnvID, parentEnvID); err != nil {
		return fmt.Errorf("copy preview-only overrides: %w", err)
	}
	return nil
}

// BumpPreviewEnvExpiry pushes a preview environment's TTL out from now, used on
// pull_request "synchronize" (a new commit pushed to the PR) so an actively
// updated preview does not get reaped mid-review.
func BumpPreviewEnvExpiry(ctx context.Context, pool *pgxpool.Pool, envID uuid.UUID, ttl time.Duration) error {
	_, err := pool.Exec(ctx, `
		UPDATE environments SET expires_at = $2, updated_at = NOW()
		WHERE id = $1 AND is_ephemeral
	`, envID, time.Now().Add(ttl))
	if err != nil {
		return fmt.Errorf("bump preview env expiry: %w", err)
	}
	return nil
}

// CountActivePreviewEnvs returns the number of ephemeral environments a project
// currently has, for the preview_env_max quota check performed before a new
// preview env is created.
func CountActivePreviewEnvs(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM environments WHERE project_id = $1 AND is_ephemeral
	`, projectID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count active preview envs: %w", err)
	}
	return n, nil
}

// PreviewEnvMax returns a project's preview_env_max quota, defaulting to 5 when
// the project has no project_quotas row yet (matches the column default set by
// migration 014).
func PreviewEnvMax(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) (int, error) {
	var max int
	err := pool.QueryRow(ctx, `
		SELECT preview_env_max FROM project_quotas WHERE project_id = $1
	`, projectID).Scan(&max)
	if err == pgx.ErrNoRows {
		return 5, nil
	}
	if err != nil {
		return 0, fmt.Errorf("preview env max: %w", err)
	}
	return max, nil
}

// createPreviewEnvPayload mirrors backend models.CreatePreviewEnvPayload. JSON
// tags are a hard contract with gitops-agent's doCreatePreviewEnv worker
// (gitops-agent/internal/worker/preview.go) - do NOT rename them.
type createPreviewEnvPayload struct {
	EnvName     string `json:"env_name"`
	Namespace   string `json:"namespace"`
	GitRepoID   string `json:"git_repo_id"`
	PRNumber    int    `json:"pr_number"`
	HeadBranch  string `json:"head_branch"`
	ParentEnvID string `json:"parent_env_id"`
}

// deletePreviewEnvPayload mirrors backend models.DeletePreviewEnvPayload. JSON
// tags are a hard contract with gitops-agent's doDeletePreviewEnv worker - do
// NOT rename them.
type deletePreviewEnvPayload struct {
	EnvironmentID string `json:"environment_id"`
	Namespace     string `json:"namespace"`
}

// InsertCreatePreviewEnvOp enqueues the CreatePreviewEnv operation that lets
// gitops-agent render the preview namespace's git-side policy file (and
// idempotently re-run the same environments upsert EnsurePreviewEnv already
// did synchronously). actor is SystemUserID for a webhook-driven PR event.
func InsertCreatePreviewEnvOp(ctx context.Context, pool *pgxpool.Pool, actor, projectID, envID uuid.UUID, envName, namespace string, gitRepoID uuid.UUID, prNumber int, headBranch string, parentEnvID uuid.UUID) (uuid.UUID, error) {
	payload, err := json.Marshal(createPreviewEnvPayload{
		EnvName:     envName,
		Namespace:   namespace,
		GitRepoID:   gitRepoID.String(),
		PRNumber:    prNumber,
		HeadBranch:  headBranch,
		ParentEnvID: parentEnvID.String(),
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal CreatePreviewEnv payload: %w", err)
	}
	var opID uuid.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		VALUES ($1, $2, $3, 'CreatePreviewEnv', 'Environment', $4, 'Created', $5)
		RETURNING id
	`, actor, projectID, envID, namespace, payload).Scan(&opID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert CreatePreviewEnv operation: %w", err)
	}
	return opID, nil
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
	return opID, nil
}

// EnvPreviewInfo returns whether an environment is ephemeral and, if so, the PR
// head branch it tracks. Used by HandoffDeploy's CreateApp branch to decide
// between the normal default-domain hostname and a per-branch preview
// hostname.
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
// when no preview environment exists for this (repo, PR) - e.g. the PR was
// opened before preview envs were enabled, or teardown already ran.
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
