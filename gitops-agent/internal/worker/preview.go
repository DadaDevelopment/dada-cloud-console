package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// previewQuota is the tight ResourceQuota applied to every preview namespace.
// Preview envs are throwaway PR environments — they get a small, fixed budget so
// a flood of PRs cannot exhaust cluster capacity. Tune via cluster policy later.
var previewResourceQuota = renderer.ResourceQuotaSpec{
	RequestsCpu:    "1",
	RequestsMemory: "2Gi",
	LimitsCpu:      "2",
	LimitsMemory:   "4Gi",
	Pods:           "10",
}

var previewLimitRange = renderer.LimitRangeSpec{
	DefaultCpu:    "250m",
	DefaultMemory: "256Mi",
	MaxCpu:        "1",
	MaxMemory:     "1Gi",
	MinMemory:     "16Mi",
}

// doCreatePreviewEnv provisions an ephemeral PR preview environment. It is
// idempotent on (git_repo_id, pr_number) via the unique partial index from
// migration 014. Steps:
//  1. insert/ensure the environments row (type=preview, is_ephemeral, namespace,
//     git_repo_id, pr_number, pr_head_branch, parent_env_id, expires_at).
//  2. render a tight namespace policy (ResourceQuota + LimitRange) into git.
//  3. copy env_vars from parent_env_id into the new environment_id.
//
// Field names in the payload MUST match what the backend writes:
// env_name, namespace, git_repo_id, pr_number, head_branch, parent_env_id.
func (w *DBWatcher) doCreatePreviewEnv(ctx context.Context, op db.Operation) error {
	var p struct {
		EnvName     string  `json:"env_name"`
		Namespace   string  `json:"namespace"`
		GitRepoID   *string `json:"git_repo_id"`
		PRNumber    *int    `json:"pr_number"`
		HeadBranch  string  `json:"head_branch"`
		ParentEnvID *string `json:"parent_env_id"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	if p.EnvName == "" || p.Namespace == "" {
		return fmt.Errorf("create preview env: env_name and namespace are required")
	}

	gitRepoID, err := optionalUUID(p.GitRepoID)
	if err != nil {
		return fmt.Errorf("git_repo_id: %w", err)
	}
	parentEnvID, err := optionalUUID(p.ParentEnvID)
	if err != nil {
		return fmt.Errorf("parent_env_id: %w", err)
	}

	expiresAt := time.Now().Add(w.cfg.PreviewEnvTTL)

	// Insert/ensure the preview environments row. ON CONFLICT (project_id, name)
	// keeps the operation idempotent if it is retried.
	var envID uuid.UUID
	err = w.pool.QueryRow(ctx, `
		INSERT INTO environments
			(project_id, name, namespace, type, is_ephemeral,
			 git_repo_id, pr_number, pr_head_branch, parent_env_id, expires_at)
		VALUES ($1, $2, $3, 'preview', TRUE, $4, $5, $6, $7, $8)
		ON CONFLICT (project_id, name) DO UPDATE
		SET namespace      = EXCLUDED.namespace,
		    is_ephemeral   = TRUE,
		    git_repo_id    = EXCLUDED.git_repo_id,
		    pr_number      = EXCLUDED.pr_number,
		    pr_head_branch = EXCLUDED.pr_head_branch,
		    parent_env_id  = EXCLUDED.parent_env_id,
		    expires_at     = EXCLUDED.expires_at,
		    updated_at     = NOW()
		RETURNING id
	`, op.ProjectID, p.EnvName, p.Namespace, gitRepoID, p.PRNumber, p.HeadBranch, parentEnvID, expiresAt).Scan(&envID)
	if err != nil {
		return fmt.Errorf("insert preview environment: %w", err)
	}

	// Copy env_vars from the parent environment so the preview inherits config.
	// Values stay encrypted (ciphertext copied verbatim — no decrypt needed).
	if parentEnvID != nil {
		if err := copyPreviewEnvVars(ctx, w.pool, envID, *parentEnvID); err != nil {
			return err
		}
	}

	// Render a tight namespace policy (ResourceQuota + LimitRange) for the preview
	// namespace, reusing the namespace_policy renderer.
	policyYAML, err := renderer.RenderNamespacePolicy(renderer.NamespacePolicySpec{
		Namespace:      p.Namespace,
		LimitRange:     previewLimitRange,
		ResourceQuota:  previewResourceQuota,
		RegistrySecret: &renderer.RegistrySecretSpec{Enabled: true},
	})
	if err != nil {
		return err
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}
	policyPath := renderer.NamespacePolicyGitPath(p.Namespace)
	commitMsg := fmt.Sprintf(
		"[DADA Console] Create preview env %s (PR #%s)\n\nOperation: %s\nProject: %s\nNamespace: %s\n",
		p.EnvName, prNumberStr(p.PRNumber), op.ID, op.ProjectID, p.Namespace,
	)
	log.Info().Str("env", p.EnvName).Str("ns", p.Namespace).Str("env_id", envID.String()).
		Msg("provisioned preview environment")
	return w.commitAndRecord(ctx, op, mgr, policyPath, policyYAML, commitMsg)
}

// copyPreviewEnvVars seeds previewEnvID's env_vars from parentEnvID's env_vars,
// preferring the value/is_secret of any matching row in
// parentEnvID's preview_env_overrides (a key present there wins over the
// inherited value for that same key), then copies override-only keys (no
// env_vars counterpart on the parent) in as ordinary runtime vars. Mirrors
// build-agent's EnsurePreviewEnv (build-agent/internal/db/preview.go) byte for
// byte so the synchronous webhook insert and this async idempotent re-run can
// never disagree about the preview env's shape.
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

// doDeletePreviewEnv tears down a preview environment: for each app in the env it
// runs the existing DeleteApp clean-prune (remove the whole app folder, which
// also drops PublicApi CRs from resources.values.yaml → cert-manager GCs the
// cert), removes the namespace-policy file, and deletes the environments row
// (FK cascade drops env_vars/deployments/builds).
//
// Payload field names MUST match the backend: environment_id, namespace.
func (w *DBWatcher) doDeletePreviewEnv(ctx context.Context, op db.Operation) error {
	var p struct {
		EnvironmentID string `json:"environment_id"`
		Namespace     string `json:"namespace"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	if p.EnvironmentID == "" {
		return fmt.Errorf("delete preview env: environment_id is required")
	}
	envID, err := uuid.Parse(p.EnvironmentID)
	if err != nil {
		return fmt.Errorf("environment_id: %w", err)
	}

	projectName, envName, namespace, err := w.projectEnv(ctx, op.ProjectID, &envID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}
	if p.Namespace == "" {
		p.Namespace = namespace
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}
	if err := mgr.EnsureCloned(); err != nil {
		return err
	}

	// Collect every app in this env from snapshots, then remove each app's whole
	// git folder (app.yaml, values.yaml, resources.values.yaml, compose.yaml, .env)
	// — the ADR-0005 clean-prune. resources.values.yaml carries the PublicApi CRs,
	// so removing it tears down domains/certs too (DeletePublicApi equivalent).
	appNames, err := w.listEnvApps(ctx, op.ProjectID, envID)
	if err != nil {
		return err
	}
	var paths []string
	for _, app := range appNames {
		paths = append(paths,
			renderer.AppGitPath(projectName, envName, app),
			renderer.AppHelmValuesGitPath(projectName, envName, app),
			renderer.AppResourcesValuesGitPath(projectName, envName, app),
			renderer.AppComposeGitPath(projectName, envName, app),
			renderer.AppEnvGitPath(projectName, envName, app),
		)
	}
	// Remove the preview namespace policy file too.
	paths = append(paths, renderer.NamespacePolicyGitPath(p.Namespace))

	commitMsg := fmt.Sprintf(
		"[DADA Console] Delete preview env %s\n\nOperation: %s\nProject: %s\nNamespace: %s\nApps: %d\n",
		envName, op.ID, projectName, p.Namespace, len(appNames),
	)
	sha, err := mgr.RemoveAndPush(paths, commitMsg, w.cfg.BotName, w.cfg.BotEmail)
	if err != nil {
		return fmt.Errorf("git remove preview env: %w", err)
	}
	if sha != "" {
		opID := op.ID
		var primary string
		if len(paths) > 0 {
			primary = paths[0]
		}
		_ = db.InsertCommit(ctx, w.pool, sha, mgr.RepoURL(), mgr.Branch(),
			primary, commitMsg, w.cfg.BotName, w.cfg.BotEmail, &opID, "agent")
	}

	var primary string
	if len(paths) > 0 {
		primary = paths[0]
	}
	if err := db.MarkCommitted(ctx, w.pool, op.ID, sha, primary); err != nil {
		return err
	}

	// Drop snapshots for this env so quota/UI reflect teardown immediately.
	_, _ = w.pool.Exec(ctx,
		`DELETE FROM resource_snapshots WHERE project_id = $1 AND environment_id = $2`,
		op.ProjectID, envID)

	// Delete the environments row. FK cascade drops env_vars / deployments / builds
	// (all reference environments(id) ON DELETE CASCADE).
	if _, err := w.pool.Exec(ctx,
		`DELETE FROM environments WHERE id = $1 AND is_ephemeral`, envID); err != nil {
		return fmt.Errorf("delete preview environment row: %w", err)
	}
	log.Info().Str("env", envName).Str("ns", p.Namespace).Str("env_id", envID.String()).
		Int("apps", len(appNames)).Msg("torn down preview environment")
	return nil
}

// listEnvApps returns the app names present in an environment, read from the App
// resource snapshots (the same source DeployImageVersion uses to re-render).
func (w *DBWatcher) listEnvApps(ctx context.Context, projectID, environmentID uuid.UUID) ([]string, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT name FROM resource_snapshots
		WHERE project_id = $1 AND environment_id = $2 AND kind = 'App'
	`, projectID, environmentID)
	if err != nil {
		return nil, fmt.Errorf("list env apps: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// optionalUUID parses an optional *string into an optional uuid value for SQL
// (nil → SQL NULL). An empty string is treated as nil.
func optionalUUID(s *string) (*uuid.UUID, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func prNumberStr(n *int) string {
	if n == nil {
		return "?"
	}
	return fmt.Sprintf("%d", *n)
}
