package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog/log"
)

// pgUniqueViolation is the Postgres SQLSTATE for a unique-constraint violation,
// used by moveApp's snapshot repoint to detect a same-named row already present
// in the target project/environment.
const pgUniqueViolation = "23505"

// doMoveApp re-homes a stateless app to another project's environment (ADR-014
// Phase 1). It never touches a stateful app: a persistent volume or an attached
// ServiceDatabaseV2 aborts the whole operation before any git write happens.
//
// Sequence:
//  1. Resolve src slug/env/namespace (op.ProjectID, op.EnvironmentID) and dst
//     slug/env/namespace (payload.TargetProjectID, payload.TargetEnvID).
//  2. Guard: reload the src App snapshot and its children; abort if a volume or
//     an attached ServiceDatabaseV2 is present.
//  3. Render the app under the dst git path — app.yaml, values.yaml, and a
//     resources.values.yaml carried over from src (its PublicApi/domain entries
//     verbatim, its Secret regenerated from decrypted env_vars, any
//     ServiceDatabaseV2 entry defensively stripped).
//  4. Copy env_vars rows to the dst environment (encrypted bytes unchanged).
//  5. Commit the dst files in one commit — this also calls db.MarkCommitted, so
//     the operation's final git_commit/git_path point at the dst commit.
//  6. Remove the src git folder in a second, forward-only commit.
//  7. Repoint resource_snapshots (App + children) to the dst project/env, then
//     delete the now-copied src env_vars.
//
// Rollback: any failure in steps 2-5 returns an error with the src fully intact
// (the caller — poll() — calls db.MarkFailed); no src removal, no snapshot
// repoint happens, and the dst render is abandoned uncommitted. Steps 6-7 run
// only after the dst commit lands. Operations are single-shot (no re-claim after
// terminal state), so a failure AFTER the dst commit is not auto-retried: the app
// is already live in dst, and a src copy may linger until the orphan-GC reaps the
// stranded src App snapshot (its git folder is gone once step 6 succeeds). If
// step 6 itself fails the src stays deployed — a visible duplicate the operator
// resolves by re-running the move; the steps are written to be safe to re-drive.
func (w *DBWatcher) doMoveApp(ctx context.Context, op db.Operation) error {
	var p struct {
		AppName         string    `json:"app_name"`
		TargetProjectID uuid.UUID `json:"target_project_id"`
		TargetEnvID     uuid.UUID `json:"target_env_id"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	if p.AppName == "" || p.TargetProjectID == uuid.Nil || p.TargetEnvID == uuid.Nil {
		return fmt.Errorf("move app: app_name, target_project_id and target_env_id are required")
	}
	if op.EnvironmentID == nil {
		return fmt.Errorf("move app %q: source operation has no environment_id", p.AppName)
	}
	srcEnvID := *op.EnvironmentID

	srcProjectSlug, srcEnvName, _, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("src project/env lookup: %w", err)
	}
	dstProjectSlug, dstEnvName, dstNamespace, err := w.projectEnv(ctx, p.TargetProjectID, &p.TargetEnvID)
	if err != nil {
		return fmt.Errorf("target project/env lookup: %w", err)
	}

	var srcRuntime, dstRuntime string
	if err := w.pool.QueryRow(ctx, `SELECT runtime FROM environments WHERE id = $1`, srcEnvID).Scan(&srcRuntime); err != nil {
		return fmt.Errorf("src runtime lookup: %w", err)
	}
	if err := w.pool.QueryRow(ctx, `SELECT runtime FROM environments WHERE id = $1`, p.TargetEnvID).Scan(&dstRuntime); err != nil {
		return fmt.Errorf("target runtime lookup: %w", err)
	}
	if srcRuntime != "k8s" || dstRuntime != "k8s" {
		return fmt.Errorf("move app %q: Phase 1 supports Kubernetes apps only (src runtime=%s, target runtime=%s)", p.AppName, srcRuntime, dstRuntime)
	}

	var summaryRaw []byte
	if err := w.pool.QueryRow(ctx, `
		SELECT summary_json FROM resource_snapshots
		WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3
	`, op.ProjectID, srcEnvID, p.AppName).Scan(&summaryRaw); err != nil {
		return fmt.Errorf("src app snapshot lookup: %w", err)
	}
	var desired struct {
		Image        string         `json:"image"`
		Framework    string         `json:"framework"`
		Port         int            `json:"port"`
		Replicas     int            `json:"replicas"`
		Profile      string         `json:"profile"`
		WorkloadType string         `json:"workload_type"`
		Volume       map[string]any `json:"volume"`
	}
	if err := json.Unmarshal(summaryRaw, &desired); err != nil {
		return fmt.Errorf("parse src app snapshot: %w", err)
	}
	if len(desired.Volume) > 0 {
		return fmt.Errorf("move app %q: has persistent storage attached; Phase 1 cannot move stateful apps (ADR-014 Phase 2)", p.AppName)
	}
	if desired.WorkloadType == "StatefulSet" {
		return fmt.Errorf("move app %q: is a StatefulSet; Phase 1 cannot move stateful apps (ADR-014 Phase 2)", p.AppName)
	}

	moveSnapshots, err := db.AppMoveSnapshots(ctx, w.pool, op.ProjectID, srcEnvID, p.AppName)
	if err != nil {
		return fmt.Errorf("load app snapshots: %w", err)
	}
	for _, ref := range moveSnapshots {
		if ref.Kind == "ServiceDatabaseV2" {
			return fmt.Errorf("move app %q: has an attached database %q; Phase 1 cannot move stateful apps (ADR-014 Phase 3)", p.AppName, ref.Name)
		}
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}
	if err := mgr.EnsureCloned(); err != nil {
		return err
	}

	env, err := w.resolveRuntimeEnv(ctx, &srcEnvID, p.AppName)
	if err != nil {
		return err
	}

	argoName := renderer.ScopedArgoName(p.AppName, dstEnvName, p.TargetProjectID.String())
	appSpec := renderer.AppSpec{
		Name:               p.AppName,
		Namespace:          dstNamespace,
		ProjectSlug:        dstProjectSlug,
		EnvSlug:            dstEnvName,
		Image:              desired.Image,
		Framework:          desired.Framework,
		Port:               desired.Port,
		Replicas:           desired.Replicas,
		Profile:            desired.Profile,
		OperationID:        op.ID.String(),
		HelmRepoURL:        mgr.RepoURL(),
		HelmTargetRevision: mgr.Branch(),
		Env:                env.Plain,
		ArgoName:           argoName,
	}
	if env.hasSecret() {
		appSpec.SecretEnvName = renderer.AppEnvSecretName(p.AppName)
		for k := range env.Secret {
			appSpec.SecretEnvKeys = append(appSpec.SecretEnvKeys, k)
		}
	}
	appYAML, err := renderer.RenderApp(appSpec)
	if err != nil {
		return err
	}
	valuesYAML, err := renderer.RenderAppValues(appSpec)
	if err != nil {
		return err
	}

	dstAppPath := renderer.AppGitPath(dstProjectSlug, dstEnvName, p.AppName)
	dstValuesPath := renderer.AppHelmValuesGitPath(dstProjectSlug, dstEnvName, p.AppName)
	files := []git.FileChange{
		{Path: dstAppPath, Content: appYAML},
		{Path: dstValuesPath, Content: valuesYAML},
	}

	srcResPath := renderer.AppResourcesValuesGitPath(srcProjectSlug, srcEnvName, p.AppName)
	dstResPath := renderer.AppResourcesValuesGitPath(dstProjectSlug, dstEnvName, p.AppName)
	rv, err := loadResourcesValues(mgr, srcResPath)
	if err != nil {
		return fmt.Errorf("load src resources.values.yaml: %w", err)
	}
	rv.RemoveKind("ServiceDatabaseV2")
	secretName := renderer.AppEnvSecretName(p.AppName)
	if env.hasSecret() {
		secretYAML, sErr := renderer.RenderAppEnvSecret(renderer.AppEnvSecretSpec{
			Name:        secretName,
			Namespace:   dstNamespace,
			ProjectSlug: dstProjectSlug,
			EnvSlug:     dstEnvName,
			OperationID: op.ID.String(),
			Data:        env.Secret,
		})
		if sErr != nil {
			return sErr
		}
		if err := rv.Upsert(secretYAML); err != nil {
			return err
		}
	} else {
		rv.Remove("Secret", secretName)
	}
	if len(rv.Manifests) > 0 {
		resContent, mErr := rv.Marshal()
		if mErr != nil {
			return mErr
		}
		files = append(files, git.FileChange{Path: dstResPath, Content: resContent})
	}

	if _, err := w.pool.Exec(ctx, `
		INSERT INTO env_vars (environment_id, app_name, key, value_encrypted, is_secret, scope)
		SELECT $1, app_name, key, value_encrypted, is_secret, scope
		FROM env_vars WHERE environment_id = $2 AND app_name = $3
		ON CONFLICT (environment_id, app_name, key) DO NOTHING
	`, p.TargetEnvID, srcEnvID, p.AppName); err != nil {
		return fmt.Errorf("copy env_vars to target: %w", err)
	}

	commitMsg := fmt.Sprintf(
		"[DADA Console] Move App %s to project %s\n\nOperation: %s\nSource: %s/%s\nTarget: %s/%s\n",
		p.AppName, dstProjectSlug, op.ID, srcProjectSlug, srcEnvName, dstProjectSlug, dstEnvName,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, dstAppPath, files, commitMsg); err != nil {
		return fmt.Errorf("commit target app: %w", err)
	}

	srcPaths := []string{
		renderer.AppGitPath(srcProjectSlug, srcEnvName, p.AppName),
		renderer.AppHelmValuesGitPath(srcProjectSlug, srcEnvName, p.AppName),
		renderer.AppResourcesValuesGitPath(srcProjectSlug, srcEnvName, p.AppName),
	}
	removeMsg := fmt.Sprintf(
		"[DADA Console] Remove App %s from project %s (moved to %s)\n\nOperation: %s\n",
		p.AppName, srcProjectSlug, dstProjectSlug, op.ID,
	)
	sha, err := mgr.RemoveAndPush(srcPaths, removeMsg, w.cfg.BotName, w.cfg.BotEmail)
	if err != nil {
		return fmt.Errorf("remove src app git folder: %w", err)
	}
	if sha != "" {
		opID := op.ID
		_ = db.InsertCommit(ctx, w.pool, sha, mgr.RepoURL(), mgr.Branch(),
			srcPaths[0], removeMsg, w.cfg.BotName, w.cfg.BotEmail, &opID, "agent")
	}

	if err := w.repointMovedAppSnapshots(ctx, op.ProjectID, srcEnvID, p.TargetProjectID, p.TargetEnvID, p.AppName); err != nil {
		return fmt.Errorf("repoint snapshots: %w", err)
	}

	if _, err := w.pool.Exec(ctx,
		`DELETE FROM env_vars WHERE environment_id = $1 AND app_name = $2`,
		srcEnvID, p.AppName,
	); err != nil {
		return fmt.Errorf("delete src env_vars: %w", err)
	}

	log.Info().Str("app", p.AppName).Str("src_project", srcProjectSlug).Str("target_project", dstProjectSlug).
		Msg("db-watcher: moved app")
	return nil
}

// repointMovedAppSnapshots re-parents the App row and every child
// resource_snapshots row (the exact set db.AppMoveSnapshots returned pre-move)
// onto the target project/environment. Per row it falls back to deleting the
// src row when the target already holds a same-named row for the same
// (kind,name) — the unique_violation path on (project_id,environment_id,kind,
// name) — so a partial prior run (crash between commit and repoint) can be
// safely re-driven to completion.
func (w *DBWatcher) repointMovedAppSnapshots(ctx context.Context, srcProjectID, srcEnvID, dstProjectID, dstEnvID uuid.UUID, appName string) error {
	refs, err := db.AppMoveSnapshots(ctx, w.pool, srcProjectID, srcEnvID, appName)
	if err != nil {
		return fmt.Errorf("load snapshots to repoint: %w", err)
	}
	if len(refs) == 0 {
		return nil
	}

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin repoint tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, ref := range refs {
		_, err := tx.Exec(ctx,
			`UPDATE resource_snapshots SET project_id = $1, environment_id = $2, last_synced_at = NOW() WHERE id = $3`,
			dstProjectID, dstEnvID, ref.ID,
		)
		if err == nil {
			continue
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			if _, derr := tx.Exec(ctx, `DELETE FROM resource_snapshots WHERE id = $1`, ref.ID); derr != nil {
				return fmt.Errorf("drop duplicate src snapshot %s/%s: %w", ref.Kind, ref.Name, derr)
			}
			continue
		}
		return fmt.Errorf("repoint snapshot %s/%s: %w", ref.Kind, ref.Name, err)
	}
	return tx.Commit(ctx)
}
