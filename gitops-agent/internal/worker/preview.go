package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// Preview (PR) environments are no longer a product feature: nothing creates
// them any more, in build-agent or here. What is left in this file is the
// teardown half, because environments opened before the removal still exist and
// still have to be taken down — by a closing PR, by the console's explicit
// delete, or by the TTL reaper. doCreatePreviewEnv and its whole helper chain
// (parent-database discovery, env-var copy-and-rewrite, owner-app snapshot
// seeding, per-preview namespace quota) are gone; a stray legacy
// CreatePreviewEnv operation now fails loudly in the dispatch switch instead of
// quietly provisioning a second copy of somebody's app.

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
