package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/dada-tuda/console/gitops-agent/internal/crypto"
	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/mlflow"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// DBWatcher polls the operations table and commits manifests to git.
type DBWatcher struct {
	pool     *pgxpool.Pool
	cfg      *config.Config
	managers map[string]*git.Manager // keyed by repoURL
	mlflow   *mlflow.Client          // nil when MLFLOW_BASE_URL is unset
}

func NewDBWatcher(pool *pgxpool.Pool, cfg *config.Config) *DBWatcher {
	defaultMgr := git.New(git.RepoConfig{
		RepoURL:   cfg.DefaultRepoURL,
		Branch:    cfg.DefaultBranch,
		Username:  cfg.DefaultUsername,
		Token:     cfg.DefaultToken,
		LocalBase: cfg.RepoLocalPath,
	})
	return &DBWatcher{
		pool: pool,
		cfg:  cfg,
		managers: map[string]*git.Manager{
			cfg.DefaultRepoURL: defaultMgr,
		},
		mlflow: mlflow.New(cfg.MLflowBaseURL, cfg.MLflowAuthHeader),
	}
}

func (w *DBWatcher) Start(ctx context.Context) {
	log.Info().Dur("interval", w.cfg.PollIntervalDB).Msg("db-watcher started")
	ticker := time.NewTicker(w.cfg.PollIntervalDB)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

// BootstrapProjects mirrors DB projects into git before the steady-state watcher starts.
// Git remains authoritative if a project.yaml already exists.
// Also bootstraps Keycloak Group CRs into the keycloak-config chart (idempotent).
func (w *DBWatcher) BootstrapProjects(ctx context.Context) error {
	projects, err := db.ListProjects(ctx, w.pool)
	if err != nil {
		return err
	}

	// Default manager writes to argo-infra; used for KC group CRs.
	defaultMgr, ok := w.managers[w.cfg.DefaultRepoURL]
	if ok {
		if err := defaultMgr.EnsureCloned(); err != nil {
			log.Warn().Err(err).Msg("db-watcher: failed to clone default repo for KC group bootstrap")
			defaultMgr = nil
		}
	}

	for _, project := range projects {
		mgr, err := w.managerFor(ctx, project.ID)
		if err != nil {
			return err
		}
		if err := mgr.EnsureCloned(); err != nil {
			return err
		}

		// Bootstrap project.yaml (idempotent — skip if already in git).
		gitPath := renderer.ProjectGitPath(project.Name)
		if _, err := mgr.ReadFile(gitPath); errors.Is(err, os.ErrNotExist) {
			yaml, err := renderer.RenderProject(renderer.ProjectSpec{
				Project:            project.Name,
				DisplayName:        project.DisplayName,
				OwnerType:          project.OwnerType,
				DefaultEnvironment: project.DefaultEnvironment,
				Quotas:             map[string]any{},
			})
			if err != nil {
				return err
			}
			commitMsg := fmt.Sprintf(
				"[DADA Console] Bootstrap project %s\n\nProject: %s\n",
				project.DisplayName, project.Name,
			)
			sha, err := mgr.CommitAndPush(gitPath, yaml, commitMsg, w.cfg.BotName, w.cfg.BotEmail)
			if err != nil {
				return err
			}
			if err := db.InsertCommit(ctx, w.pool,
				sha, mgr.RepoURL(), mgr.Branch(), gitPath, commitMsg,
				w.cfg.BotName, w.cfg.BotEmail, nil, "agent",
			); err != nil {
				log.Warn().Err(err).Str("project", project.Name).Msg("db-watcher: record bootstrap commit")
			}
			log.Info().Str("project", project.Name).Str("path", gitPath).Str("sha", sha).Msg("db-watcher: bootstrapped project manifest")
		} else if err != nil {
			return err
		} else {
			log.Debug().Str("project", project.Name).Str("path", gitPath).Msg("db-watcher: project already present in git")
		}

		// Bootstrap Keycloak Group CRs (idempotent — skip if already in git).
		// Requires default manager (argo-infra); skip gracefully if unavailable.
		if defaultMgr == nil {
			continue
		}
		kcPath := renderer.ProjectGroupsGitPath(project.Name)
		if _, err := defaultMgr.ReadFile(kcPath); errors.Is(err, os.ErrNotExist) {
			members, err := db.ListProjectMembers(ctx, w.pool, project.Name)
			if err != nil {
				log.Warn().Err(err).Str("project", project.Name).Msg("db-watcher: list members for KC bootstrap")
				continue
			}
			memberMap := make(map[string]string, len(members))
			for _, m := range members {
				memberMap[m.Username] = m.Role
			}
			kcYAML, err := renderer.RenderProjectGroups(renderer.ProjectGroupSpec{
				ProjectSlug: project.Name,
				Members:     memberMap,
			})
			if err != nil {
				log.Warn().Err(err).Str("project", project.Name).Msg("db-watcher: render KC groups")
				continue
			}
			commitMsg := fmt.Sprintf("[DADA Console] Bootstrap KC groups for project %s\n", project.Name)
			sha, err := defaultMgr.CommitAndPush(kcPath, kcYAML, commitMsg, w.cfg.BotName, w.cfg.BotEmail)
			if err != nil {
				log.Warn().Err(err).Str("project", project.Name).Msg("db-watcher: commit KC groups")
				continue
			}
			log.Info().Str("project", project.Name).Str("path", kcPath).Str("sha", sha).Msg("db-watcher: bootstrapped KC group CRs")
		} else if err != nil {
			log.Warn().Err(err).Str("project", project.Name).Msg("db-watcher: check KC group file")
		} else {
			log.Debug().Str("project", project.Name).Str("path", kcPath).Msg("db-watcher: KC groups already in git")
		}
	}

	return nil
}

func (w *DBWatcher) poll(ctx context.Context) {
	ops, err := db.ClaimPending(ctx, w.pool)
	if err != nil {
		log.Error().Err(err).Msg("db-watcher: claim pending")
		return
	}
	for _, op := range ops {
		if err := w.dispatch(ctx, op); err != nil {
			log.Error().Err(err).Str("op", op.ID.String()).Str("action", op.Action).Msg("operation failed")
			_ = db.MarkFailed(ctx, w.pool, op.ID, "PROCESSING_ERROR", err.Error())
		}
	}
}

func (w *DBWatcher) dispatch(ctx context.Context, op db.Operation) error {
	switch op.Action {
	case "CreateServiceDatabase":
		return w.doCreateServiceDatabase(ctx, op)
	case "CreateApp":
		return w.doCreateApp(ctx, op)
	case "DeleteApp":
		return w.doDeleteApp(ctx, op)
	case "DeployImageVersion":
		return w.doDeployImageVersion(ctx, op)
	case "CreatePublicApi":
		return w.doCreatePublicApi(ctx, op)
	case "AttachCustomHostname":
		return w.doAttachCustomHostname(ctx, op)
	case "DetachCustomHostname":
		return w.doDetachCustomHostname(ctx, op)
	case "CreateAIModel":
		return w.doCreateAIModel(ctx, op)
	case "UpdateAIModelArtifact":
		return w.doUpdateAIModelArtifact(ctx, op)
	case "SetCanaryTraffic":
		return w.doSetCanaryTraffic(ctx, op)
	case "PromoteAIModel":
		return w.doPromoteAIModel(ctx, op)
	case "DeleteAIModel":
		return w.doDeleteAIModel(ctx, op)
	case "PinAIModelMlflowVersion":
		return w.doPinAIModelMlflowVersion(ctx, op)
	case "SetNamespacePolicy":
		return w.doSetNamespacePolicy(ctx, op)
	case "CreateS3Bucket":
		return w.doCreateS3Bucket(ctx, op)
	case "CreatePreviewEnv":
		return w.doCreatePreviewEnv(ctx, op)
	case "DeletePreviewEnv":
		return w.doDeletePreviewEnv(ctx, op)
	case "ImportComposeStack":
		return w.doImportComposeStack(ctx, op)
	default:
		return fmt.Errorf("unknown action: %s", op.Action)
	}
}

// projectEnv fetches project name, env name, and env namespace from the DB.
func (w *DBWatcher) projectEnv(ctx context.Context, projectID uuid.UUID, environmentID *uuid.UUID) (projectName, envName, envNamespace string, err error) {
	err = w.pool.QueryRow(ctx, `
		SELECT p.name, e.name, e.namespace
		FROM projects p JOIN environments e ON e.project_id = p.id
		WHERE p.id = $1 AND e.id = $2
	`, projectID, environmentID).Scan(&projectName, &envName, &envNamespace)
	return
}

// managerFor returns the git.Manager for a project, creating one if needed.
func (w *DBWatcher) managerFor(ctx context.Context, projectID uuid.UUID) (*git.Manager, error) {
	integration, err := db.GetIntegration(ctx, w.pool, projectID)
	if err != nil {
		return nil, err
	}
	return w.managerForIntegration(projectID, integration)
}

// managerForIntegration resolves the git manager for a project once the
// project-specific integration row (if any) is already known.
func (w *DBWatcher) managerForIntegration(projectID uuid.UUID, integration *db.GitIntegration) (*git.Manager, error) {
	if integration == nil {
		// Shared-repo mode: projects without a dedicated git_integration still use
		// the platform default GitOps repo. Broken integrations must not silently
		// fall back here.
		return w.managers[w.cfg.DefaultRepoURL], nil
	}
	if mgr, ok := w.managers[integration.RepoURL]; ok {
		return mgr, nil
	}

	token, err := crypto.DecryptToken(w.cfg.EncryptionKey, integration.TokenEncrypted)
	if err != nil {
		return nil, fmt.Errorf("decrypt git integration token for project %s: %w", projectID, err)
	}

	mgr := git.New(git.RepoConfig{
		RepoURL:   integration.RepoURL,
		Branch:    integration.Branch,
		Username:  integration.Provider, // provider name used as username for token auth
		Token:     token,
		LocalBase: w.cfg.RepoLocalPath,
	})
	w.managers[integration.RepoURL] = mgr
	return mgr, nil
}

func (w *DBWatcher) commitAndRecord(ctx context.Context, op db.Operation, mgr *git.Manager, gitPath, content, commitMsg string) error {
	return w.commitFilesAndRecord(ctx, op, mgr, gitPath, []git.FileChange{
		{Path: gitPath, Content: content},
	}, commitMsg)
}

func (w *DBWatcher) commitFilesAndRecord(ctx context.Context, op db.Operation, mgr *git.Manager, primaryGitPath string, files []git.FileChange, commitMsg string) error {
	sha, err := mgr.CommitFilesAndPush(files, commitMsg, w.cfg.BotName, w.cfg.BotEmail)
	if err != nil {
		return fmt.Errorf("git push: %w", err)
	}

	opID := op.ID
	if err := db.InsertCommit(ctx, w.pool,
		sha, mgr.RepoURL(), mgr.Branch(), primaryGitPath, commitMsg,
		w.cfg.BotName, w.cfg.BotEmail, &opID, "agent",
	); err != nil {
		log.Warn().Err(err).Msg("recording git_commit row")
	}

	return db.MarkCommitted(ctx, w.pool, op.ID, sha, primaryGitPath)
}

// ensureAppExists returns the FileChanges needed to create the owning app's
// app.yaml + values.yaml when they are not yet present in git. When the
// app already exists — whether a bare chart-owner or a real workload — it returns
// nil so the existing definition is left untouched. Child resources
// (ServiceDatabase, AIModel, PublicApi) call this so that creating a resource
// first auto-provisions the app that owns its chart.
func (w *DBWatcher) ensureAppExists(mgr *git.Manager, projectName, envName, appName, namespace, operationID string) ([]git.FileChange, error) {
	if err := mgr.EnsureCloned(); err != nil {
		return nil, err
	}
	appPath := renderer.AppGitPath(projectName, envName, appName)
	if _, err := mgr.ReadFile(appPath); err == nil {
		return nil, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	appYAML, err := renderer.RenderApp(renderer.AppSpec{
		Name:               appName,
		Namespace:          namespace,
		ProjectSlug:        projectName,
		EnvSlug:            envName,
		OperationID:        operationID,
		HelmRepoURL:        mgr.RepoURL(),
		HelmTargetRevision: mgr.Branch(),
	})
	if err != nil {
		return nil, err
	}
	// No per-app resources/ chart is seeded anymore (ADR 0005). app.yaml sets
	// spec.resources: true; the shared helm/app-resources chart renders the app's
	// resources.values.yaml, which is created lazily on the first Upsert and is
	// safely absent until then (ignoreMissingValueFiles: true).
	return []git.FileChange{
		{Path: appPath, Content: appYAML},
		{Path: renderer.AppHelmValuesGitPath(projectName, envName, appName), Content: renderer.RenderBareAppValues()},
	}, nil
}

func (w *DBWatcher) doCreateServiceDatabase(ctx context.Context, op db.Operation) error {
	var p struct {
		Name            string `json:"name"`
		Database        string `json:"database"`
		AppRef          string `json:"app_ref"`
		BackupEnabled   bool   `json:"backup_enabled"`
		BackupSchedule  string `json:"backup_schedule"`
		BackupRetention string `json:"backup_retention"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}

	projectName, envName, envNamespace, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}

	yaml, err := renderer.RenderServiceDatabase(renderer.ServiceDatabaseSpec{
		Name:            p.Name,
		Namespace:       envNamespace,
		ProjectSlug:     projectName,
		EnvSlug:         envName,
		AppRef:          p.AppRef,
		Database:        p.Database,
		BackupEnabled:   p.BackupEnabled,
		BackupSchedule:  defaultIfEmpty(p.BackupSchedule, "daily"),
		BackupRetention: defaultIfEmpty(p.BackupRetention, "14d"),
		OperationID:     op.ID.String(),
	})
	if err != nil {
		return err
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}

	// Owner app: the bound app (app_ref) or — when standalone — the shared
	// per-project "service-databases-<project>" chart.
	ownerApp := renderer.ServiceDatabaseOwnerApp(p.AppRef, projectName)

	// Ensure the owning app exists, then upsert the CR into its
	// resources.values.yaml (keyed by kind+name).
	appFiles, err := w.ensureAppExists(mgr, projectName, envName, ownerApp, envNamespace, op.ID.String())
	if err != nil {
		return err
	}
	valuesPath := renderer.ServiceDatabaseResourcesValuesGitPath(projectName, envName, p.AppRef)
	manifestFile, err := upsertManifestFile(mgr, valuesPath, yaml)
	if err != nil {
		return err
	}
	files := append(appFiles, manifestFile)

	commitMsg := fmt.Sprintf(
		"[DADA Console] Create ServiceDatabaseV2 %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.Name, op.ID, projectName, envName,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, valuesPath, files, commitMsg); err != nil {
		return err
	}

	// Upsert snapshot immediately so the database appears in the console UI without
	// waiting for the next gitwatcher poll cycle.
	summaryJSON, _ := json.Marshal(map[string]any{
		"name":     p.Name,
		"kind":     "ServiceDatabaseV2",
		"app_ref":  p.AppRef,
		"database": p.Database,
		"status":   "Pending",
		"spec": map[string]any{
			"appRef":    p.AppRef,
			"namespace": envNamespace,
			"database":  p.Database,
			"backup": map[string]any{
				"enabled":   p.BackupEnabled,
				"frequency": p.BackupSchedule,
				"retention": p.BackupRetention,
			},
		},
	})
	return db.UpsertSnapshot(ctx, w.pool,
		op.ProjectID, op.EnvironmentID,
		"ServiceDatabaseV2", p.Name, "Pending", summaryJSON, time.Now(),
	)
}

func (w *DBWatcher) doCreateApp(ctx context.Context, op db.Operation) error {
	var p struct {
		Name          string `json:"name"`
		Image         string `json:"image"`
		Port          int    `json:"port"`
		Replicas      int    `json:"replicas"`
		Profile       string `json:"profile"`
		AppServerName string `json:"app_server_name"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}

	// VM environments deploy as a Docker Compose stack (signalled by an AppServer
	// binding) rather than a Helm App.
	if p.AppServerName != "" {
		return w.doCreateComposeApp(ctx, op, p.Name)
	}

	projectName, envName, envNamespace, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}

	// Resolve runtime env at render time (decrypted from env_vars; NEVER from the
	// plaintext operations.payload). Non-sensitive → values.yaml env:; sensitive →
	// a per-app k8s Secret CR upserted into resources.values.yaml (chart envFrom's it).
	env, err := w.resolveRuntimeEnv(ctx, op.EnvironmentID, p.Name)
	if err != nil {
		return err
	}

	appSpec := renderer.AppSpec{
		Name:               p.Name,
		Namespace:          envNamespace,
		ProjectSlug:        projectName,
		EnvSlug:            envName,
		Image:              p.Image,
		Port:               p.Port,
		Replicas:           p.Replicas,
		Profile:            p.Profile,
		OperationID:        op.ID.String(),
		HelmRepoURL:        mgr.RepoURL(),
		HelmTargetRevision: mgr.Branch(),
		Env:                env.Plain,
	}
	if env.hasSecret() {
		appSpec.SecretEnvName = renderer.AppEnvSecretName(p.Name)
	}
	yaml, err := renderer.RenderApp(appSpec)
	if err != nil {
		return err
	}
	valuesYAML, err := renderer.RenderAppValues(appSpec)
	if err != nil {
		return err
	}

	gitPath := renderer.AppGitPath(projectName, envName, p.Name)
	valuesPath := renderer.AppHelmValuesGitPath(projectName, envName, p.Name)
	files := []git.FileChange{
		{Path: gitPath, Content: yaml},
		{Path: valuesPath, Content: valuesYAML},
	}
	secretFile, err := w.renderEnvSecretFile(mgr, projectName, envName, envNamespace, p.Name, op.ID.String(), env)
	if err != nil {
		return err
	}
	if secretFile != nil {
		files = append(files, *secretFile)
	}
	commitMsg := fmt.Sprintf(
		"[DADA Console] Create App %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.Name, op.ID, projectName, envName,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, gitPath, files, commitMsg); err != nil {
		return err
	}

	// Upsert snapshot so DeployImageVersion can re-render without reading git.
	summaryJSON, _ := json.Marshal(map[string]any{
		"image": p.Image, "port": p.Port, "replicas": p.Replicas,
		"profile": p.Profile, "status": "Pending",
	})
	return db.UpsertSnapshot(ctx, w.pool,
		op.ProjectID, op.EnvironmentID,
		"App", p.Name, "Pending", summaryJSON, time.Now(),
	)
}

// doDeleteApp removes an app's entire git folder in one commit: app.yaml,
// values.yaml, and resources.values.yaml. This is safe under ADR 0005 because
// resources is now a values file (ignoreMissingValueFiles), not a path source,
// so ArgoCD prunes cleanly with no wedge. Missing files are skipped silently by
// RemoveAndPush. Also clears the app's own snapshot AND every child resource
// snapshot bound to it (ServiceDatabaseV2 / PublicApi / S3Bucket / AIModel), and
// revokes any AIModel API keys bound to the app, so quota/read APIs reflect the
// deletion immediately.
func (w *DBWatcher) doDeleteApp(ctx context.Context, op db.Operation) error {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}

	projectName, envName, _, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}
	if err := mgr.EnsureCloned(); err != nil {
		return err
	}

	paths := []string{
		renderer.AppGitPath(projectName, envName, p.Name),
		renderer.AppHelmValuesGitPath(projectName, envName, p.Name),
		renderer.AppResourcesValuesGitPath(projectName, envName, p.Name),
		renderer.AppComposeGitPath(projectName, envName, p.Name),
		renderer.AppEnvGitPath(projectName, envName, p.Name),
	}
	commitMsg := fmt.Sprintf(
		"[DADA Console] Delete App %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.Name, op.ID, projectName, envName,
	)
	sha, err := mgr.RemoveAndPush(paths, commitMsg, w.cfg.BotName, w.cfg.BotEmail)
	if err != nil {
		return fmt.Errorf("git remove: %w", err)
	}
	if sha != "" {
		opID := op.ID
		_ = db.InsertCommit(ctx, w.pool, sha, mgr.RepoURL(), mgr.Branch(),
			paths[0], commitMsg, w.cfg.BotName, w.cfg.BotEmail, &opID, "agent")
	}
	if err := db.MarkCommitted(ctx, w.pool, op.ID, sha, paths[0]); err != nil {
		return err
	}

	// Revoke any active AIModel API keys bound to this app (attached models).
	_, _ = w.pool.Exec(ctx,
		`UPDATE aimodel_api_keys k SET revoked_at = NOW()
		 FROM resource_snapshots s
		 WHERE k.project_id = $1 AND k.environment_id = $2 AND k.revoked_at IS NULL
		   AND s.project_id = k.project_id AND s.environment_id = k.environment_id
		   AND s.kind = 'AIModel' AND s.name = k.aimodel_name
		   AND s.summary_json->>'attached_app' = $3`,
		op.ProjectID, op.EnvironmentID, p.Name,
	)
	// Drop every child resource snapshot bound to this app (ServiceDatabaseV2,
	// PublicApi, S3Bucket, AIModel) so quota/UI state reflects the cascade. The
	// owning-app link lives under different summary keys depending on the writer:
	// API writers stamp a top-level app_ref/attached_app; the gitwatcher
	// reverse-sync stamps the CR spec (spec.appRef / spec.attachedApp /
	// spec.serviceName). Match any of them, scoped to this project+env.
	_, _ = w.pool.Exec(ctx,
		`DELETE FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind <> 'App'
		   AND (
		        summary_json->>'app_ref'            = $3
		     OR summary_json->>'attached_app'       = $3
		     OR summary_json->'spec'->>'appRef'     = $3
		     OR summary_json->'spec'->>'attachedApp' = $3
		     OR summary_json->'spec'->>'serviceName' = $3
		   )`,
		op.ProjectID, op.EnvironmentID, p.Name,
	)
	// Drop the App snapshot so quota recalculation reflects deletion.
	_, _ = w.pool.Exec(ctx,
		`DELETE FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		op.ProjectID, op.EnvironmentID, p.Name,
	)
	return nil
}

// doCreateComposeApp renders a skeleton compose.yaml + .env into the app's git
// tree, records a snapshot, and enqueues a DeployStack op for the portainer-agent
// to deploy onto the environment's AppServer endpoint.
func (w *DBWatcher) doCreateComposeApp(ctx context.Context, op db.Operation, appName string) error {
	projectName, envName, _, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}

	// Resolve runtime env for the compose .env. Compose has no out-of-band secret
	// channel, so sensitive + non-sensitive are merged into the .env (which is
	// committed to git — same plaintext-in-git caveat as the k8s Secret path).
	env, err := w.resolveRuntimeEnv(ctx, op.EnvironmentID, appName)
	if err != nil {
		return err
	}

	composePath := renderer.AppComposeGitPath(projectName, envName, appName)
	envPath := renderer.AppEnvGitPath(projectName, envName, appName)
	commitMsg := fmt.Sprintf(
		"[DADA Console] Create compose app %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		appName, op.ID, projectName, envName,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, composePath, []git.FileChange{
		{Path: composePath, Content: renderer.RenderComposeSkeleton(appName)},
		{Path: envPath, Content: renderer.RenderEnvFile(env.merged())},
	}, commitMsg); err != nil {
		return err
	}

	summaryJSON, _ := json.Marshal(map[string]any{
		"runtime": "compose", "status": "Pending",
	})
	if err := db.UpsertSnapshot(ctx, w.pool,
		op.ProjectID, op.EnvironmentID,
		"App", appName, "Pending", summaryJSON, time.Now(),
	); err != nil {
		return err
	}

	deployID, err := db.EnqueueDeployStack(ctx, w.pool, op.ID, appName)
	if err != nil {
		return fmt.Errorf("enqueue deploy stack: %w", err)
	}
	log.Info().Str("app", appName).Str("deploy_op", deployID.String()).Msg("compose app rendered; deploy enqueued")
	return nil
}

// doImportComposeStack adopts a discovered VM workload (DiscoverWorkload) into
// a managed compose App: it renders compose.yaml from the included services —
// pinning any referenced named volume external so the first deploy attaches
// existing prod data instead of creating an empty one (see
// renderer.RenderComposeFromDiscovery) — writes the .env from the payload's
// EnvVars verbatim (the API handler already enforced the ack_secrets_in_git
// consent gate), commits both, records a Pending snapshot, and enqueues the
// same DeployStack op a normal compose CreateApp uses so the imported stack
// gets live state/logs/metrics like any other app.
//
// SEAM: unlike doCreateComposeApp, env vars here are NOT also persisted into
// the env_vars table (resolveRuntimeEnv), so a later "edit env" / redeploy
// through the normal app env UI will not see these values until someone sets
// them there explicitly. Import only guarantees the FIRST deploy's .env
// matches what was discovered/typed at import time.
func (w *DBWatcher) doImportComposeStack(ctx context.Context, op db.Operation) error {
	var p struct {
		AppName    string                       `json:"app_name"`
		ServerName string                       `json:"server_name"`
		Services   []renderer.ImportServiceSpec `json:"services"`
		EnvVars    map[string]string            `json:"env_vars"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}

	projectName, envName, _, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}

	composeYAML, err := renderer.RenderComposeFromDiscovery(p.Services, len(p.EnvVars) > 0)
	if err != nil {
		return err
	}

	composePath := renderer.AppComposeGitPath(projectName, envName, p.AppName)
	envPath := renderer.AppEnvGitPath(projectName, envName, p.AppName)
	commitMsg := fmt.Sprintf(
		"[DADA Console] Import compose stack %s from %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.AppName, p.ServerName, op.ID, projectName, envName,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, composePath, []git.FileChange{
		{Path: composePath, Content: composeYAML},
		{Path: envPath, Content: renderer.RenderEnvFile(p.EnvVars)},
	}, commitMsg); err != nil {
		return err
	}

	summaryJSON, _ := json.Marshal(map[string]any{
		"runtime": "compose", "status": "Pending", "imported_from": p.ServerName,
	})
	if err := db.UpsertSnapshot(ctx, w.pool,
		op.ProjectID, op.EnvironmentID,
		"App", p.AppName, "Pending", summaryJSON, time.Now(),
	); err != nil {
		return err
	}

	deployID, err := db.EnqueueDeployStack(ctx, w.pool, op.ID, p.AppName)
	if err != nil {
		return fmt.Errorf("enqueue deploy stack: %w", err)
	}
	log.Info().Str("app", p.AppName).Str("server", p.ServerName).Str("deploy_op", deployID.String()).
		Msg("compose stack imported; deploy enqueued")
	return nil
}

func (w *DBWatcher) doDeployImageVersion(ctx context.Context, op db.Operation) error {
	var p struct {
		AppName string `json:"app_name"`
		Image   string `json:"image"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}

	projectName, envName, envNamespace, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}

	var summaryRaw []byte
	if err := w.pool.QueryRow(ctx, `
		SELECT summary_json FROM resource_snapshots
		WHERE project_id=$1 AND environment_id=$2 AND kind='App' AND name=$3
	`, op.ProjectID, op.EnvironmentID, p.AppName).Scan(&summaryRaw); err != nil {
		return fmt.Errorf("loading app snapshot: %w", err)
	}
	var cur map[string]any
	_ = json.Unmarshal(summaryRaw, &cur)

	portVal, _ := cur["port"].(float64)
	replicasVal, _ := cur["replicas"].(float64)
	profileVal, _ := cur["profile"].(string)
	if portVal == 0 {
		portVal = 8080
	}
	if replicasVal == 0 {
		replicasVal = 2
	}
	if profileVal == "" {
		profileVal = "small"
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}

	// Re-resolve runtime env on every deploy so env-var edits take effect on the
	// next deploy. Decrypted at render time; never sourced from operations.payload.
	env, err := w.resolveRuntimeEnv(ctx, op.EnvironmentID, p.AppName)
	if err != nil {
		return err
	}

	appSpec := renderer.AppSpec{
		Name:               p.AppName,
		Namespace:          envNamespace,
		ProjectSlug:        projectName,
		EnvSlug:            envName,
		Image:              p.Image,
		Port:               int(portVal),
		Replicas:           int(replicasVal),
		Profile:            profileVal,
		OperationID:        op.ID.String(),
		HelmRepoURL:        mgr.RepoURL(),
		HelmTargetRevision: mgr.Branch(),
		Env:                env.Plain,
	}
	if env.hasSecret() {
		appSpec.SecretEnvName = renderer.AppEnvSecretName(p.AppName)
	}
	yaml, err := renderer.RenderApp(appSpec)
	if err != nil {
		return err
	}
	valuesYAML, err := renderer.RenderAppValues(appSpec)
	if err != nil {
		return err
	}

	gitPath := renderer.AppGitPath(projectName, envName, p.AppName)
	valuesPath := renderer.AppHelmValuesGitPath(projectName, envName, p.AppName)
	files := []git.FileChange{
		{Path: gitPath, Content: yaml},
		{Path: valuesPath, Content: valuesYAML},
	}
	secretFile, err := w.renderEnvSecretFile(mgr, projectName, envName, envNamespace, p.AppName, op.ID.String(), env)
	if err != nil {
		return err
	}
	if secretFile != nil {
		files = append(files, *secretFile)
	}
	commitMsg := fmt.Sprintf(
		"[DADA Console] Deploy image %s for app %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.Image, p.AppName, op.ID, projectName, envName,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, gitPath, files, commitMsg); err != nil {
		return err
	}

	cur["image"] = p.Image
	cur["status"] = "Pending"
	updatedJSON, _ := json.Marshal(cur)
	return db.UpsertSnapshot(ctx, w.pool,
		op.ProjectID, op.EnvironmentID,
		"App", p.AppName, "Pending", updatedJSON, time.Now(),
	)
}

func (w *DBWatcher) doCreatePublicApi(ctx context.Context, op db.Operation) error {
	var p struct {
		AppName        string   `json:"app_name"`
		PublicApiName  string   `json:"public_api_name"`
		FQDN           string   `json:"fqdn"`
		AuthEnabled    bool     `json:"auth_enabled"`
		AuthScheme     string   `json:"auth_scheme"`
		AuthScopes     []string `json:"auth_scopes"`
		SwaggerEnabled bool     `json:"swagger_enabled"`
		SwaggerPath    string   `json:"swagger_path"`
		SwaggerTitle   string   `json:"swagger_title"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}

	projectName, envName, envNamespace, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}

	// Read app port from snapshot
	var summaryRaw []byte
	if err := w.pool.QueryRow(ctx, `
		SELECT summary_json FROM resource_snapshots
		WHERE project_id=$1 AND environment_id=$2 AND kind='App' AND name=$3
	`, op.ProjectID, op.EnvironmentID, p.AppName).Scan(&summaryRaw); err != nil {
		return fmt.Errorf("loading app snapshot: %w", err)
	}
	var appSpec map[string]any
	_ = json.Unmarshal(summaryRaw, &appSpec)
	portVal, _ := appSpec["port"].(float64)
	if portVal == 0 {
		portVal = 8080
	}

	yaml, err := renderer.RenderPublicApi(renderer.PublicApiSpec{
		Name:           p.PublicApiName,
		Namespace:      envNamespace,
		ProjectSlug:    projectName,
		EnvSlug:        envName,
		ServiceName:    p.AppName,
		ServicePort:    int(portVal),
		FQDN:           p.FQDN,
		LBTarget:       w.cfg.ClusterLBIP,
		AuthEnabled:    p.AuthEnabled,
		AuthScheme:     p.AuthScheme,
		AuthScopes:     p.AuthScopes,
		SwaggerEnabled: p.SwaggerEnabled,
		SwaggerPath:    p.SwaggerPath,
		SwaggerTitle:   p.SwaggerTitle,
		OperationID:    op.ID.String(),
	})
	if err != nil {
		return err
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}

	appFiles, err := w.ensureAppExists(mgr, projectName, envName, p.AppName, envNamespace, op.ID.String())
	if err != nil {
		return err
	}
	valuesPath := renderer.PublicApiResourcesValuesGitPath(projectName, envName, p.AppName)
	manifestFile, err := upsertManifestFile(mgr, valuesPath, yaml)
	if err != nil {
		return err
	}
	files := append(appFiles, manifestFile)

	commitMsg := fmt.Sprintf(
		"[DADA Console] Register domain %s for app %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.FQDN, p.AppName, op.ID, projectName, envName,
	)
	return w.commitFilesAndRecord(ctx, op, mgr, valuesPath, files, commitMsg)
}

// doAttachCustomHostname renders a native Ingress (cert-manager letsencrypt-prod,
// HTTP-01) for a user-owned hostname and upserts it into the owning app's
// resources.values.yaml manifests list. No Beget/PublicApi — the user owns their
// DNS and points the hostname at our ingress-nginx-pub LB.
func (w *DBWatcher) doAttachCustomHostname(ctx context.Context, op db.Operation) error {
	var p struct {
		AppName  string `json:"app_name"`
		Hostname string `json:"hostname"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}

	projectName, envName, envNamespace, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}

	// The native Ingress must target the app's real Service. The common subchart
	// names it "<app>-service" with a single port named "http" (the numeric port
	// varies per app, so reference it by name). The PublicApi path passes the bare
	// app name because its composition adds the suffix itself — a native Ingress
	// has no composition, so resolve the service name + port name here.
	yaml, err := renderer.RenderCustomIngress(renderer.CustomIngressSpec{
		Name:            renderer.FQDNToName(p.Hostname),
		Namespace:       envNamespace,
		ProjectSlug:     projectName,
		EnvSlug:         envName,
		Hostname:        p.Hostname,
		ServiceName:     renderer.AppServiceName(p.AppName),
		ServicePortName: renderer.DefaultAppServicePortName,
		OperationID:     op.ID.String(),
	})
	if err != nil {
		return err
	}

	appFiles, err := w.ensureAppExists(mgr, projectName, envName, p.AppName, envNamespace, op.ID.String())
	if err != nil {
		return err
	}
	valuesPath := renderer.AppResourcesValuesGitPath(projectName, envName, p.AppName)
	manifestFile, err := upsertManifestFile(mgr, valuesPath, yaml)
	if err != nil {
		return err
	}
	files := append(appFiles, manifestFile)

	commitMsg := fmt.Sprintf(
		"[DADA Console] Attach custom domain %s to app %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.Hostname, p.AppName, op.ID, projectName, envName,
	)
	return w.commitFilesAndRecord(ctx, op, mgr, valuesPath, files, commitMsg)
}

// doDetachCustomHostname removes the {Ingress, <host-as-name>} entry from the
// owning app's resources.values.yaml manifests list. cert-manager then GCs the
// Certificate/secret once the Ingress is pruned.
func (w *DBWatcher) doDetachCustomHostname(ctx context.Context, op db.Operation) error {
	var p struct {
		AppName  string `json:"app_name"`
		Hostname string `json:"hostname"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}

	projectName, envName, _, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}
	if err := mgr.EnsureCloned(); err != nil {
		return err
	}

	valuesPath := renderer.AppResourcesValuesGitPath(projectName, envName, p.AppName)
	manifestFile, changed, err := removeManifestsFile(mgr, valuesPath, [][2]string{
		{"Ingress", renderer.FQDNToName(p.Hostname)},
	})
	if err != nil {
		return fmt.Errorf("remove manifests: %w", err)
	}
	commitMsg := fmt.Sprintf(
		"[DADA Console] Detach custom domain %s from app %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.Hostname, p.AppName, op.ID, projectName, envName,
	)
	var sha string
	if changed {
		sha, err = mgr.CommitFilesAndPush([]git.FileChange{manifestFile}, commitMsg, w.cfg.BotName, w.cfg.BotEmail)
		if err != nil {
			return fmt.Errorf("git push (remove manifests): %w", err)
		}
		opID := op.ID
		_ = db.InsertCommit(ctx, w.pool, sha, mgr.RepoURL(), mgr.Branch(),
			valuesPath, commitMsg, w.cfg.BotName, w.cfg.BotEmail, &opID, "agent")
	}
	return db.MarkCommitted(ctx, w.pool, op.ID, sha, valuesPath)
}

func (w *DBWatcher) doSetNamespacePolicy(ctx context.Context, op db.Operation) error {
	var p struct {
		LimitRange    renderer.LimitRangeSpec    `json:"limit_range"`
		ResourceQuota renderer.ResourceQuotaSpec `json:"resource_quota"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}

	// Look up the namespace for this environment.
	var namespace string
	if err := w.pool.QueryRow(ctx, `
		SELECT e.namespace FROM environments e WHERE e.id = $1
	`, op.EnvironmentID).Scan(&namespace); err != nil {
		return fmt.Errorf("namespace lookup: %w", err)
	}

	spec := renderer.NamespacePolicySpec{
		Namespace:     namespace,
		LimitRange:    p.LimitRange,
		ResourceQuota: p.ResourceQuota,
	}
	yaml, err := renderer.RenderNamespacePolicy(spec)
	if err != nil {
		return err
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}

	gitPath := renderer.NamespacePolicyGitPath(namespace)
	commitMsg := fmt.Sprintf(
		"[DADA Console] Set namespace policy for %s\n\nOperation: %s\nProject: %s\n",
		namespace, op.ID, op.ProjectID,
	)
	return w.commitAndRecord(ctx, op, mgr, gitPath, yaml, commitMsg)
}

func (w *DBWatcher) doCreateS3Bucket(ctx context.Context, op db.Operation) error {
	var p struct {
		Name          string `json:"name"`
		BucketName    string `json:"bucket_name"`
		Region        string `json:"region"`
		Description   string `json:"description"`
		Public        bool   `json:"public"`
		FtpSftpEnable bool   `json:"ftp_sftp_enable"`
		AppRef        string `json:"app_ref"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}

	projectName, envName, envNamespace, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}

	yaml, err := renderer.RenderS3Bucket(renderer.S3BucketSpec{
		Name:          p.Name,
		BucketName:    p.BucketName,
		Region:        defaultIfEmpty(p.Region, "ru1"),
		Description:   p.Description,
		Public:        p.Public,
		FtpSftpEnable: p.FtpSftpEnable,
		ProjectSlug:   projectName,
		EnvSlug:       envName,
		OperationID:   op.ID.String(),
	})
	if err != nil {
		return err
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}

	// Owner app: the bound app (app_ref) or the per-project standalone
	// "s3-buckets-<project>" chart.
	ownerApp := renderer.S3BucketOwnerApp(p.AppRef, projectName)

	// Auto-provision the owning app if it doesn't exist yet. For an explicit
	// app_ref this is a no-op when the app already exists; for the standalone
	// "s3-buckets-<project>" app it bootstraps a bare app. Then upsert the CR
	// into the owner's resources.values.yaml (keyed by kind+name).
	ownerFiles, err := w.ensureAppExists(mgr, projectName, envName, ownerApp, envNamespace, op.ID.String())
	if err != nil {
		return err
	}
	valuesPath := renderer.S3BucketResourcesValuesGitPath(projectName, envName, p.AppRef)
	manifestFile, err := upsertManifestFile(mgr, valuesPath, yaml)
	if err != nil {
		return err
	}
	files := append(ownerFiles, manifestFile)

	commitMsg := fmt.Sprintf(
		"[DADA Console] Create S3Bucket %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\nOwner: %s\n",
		p.Name, op.ID, projectName, envName, ownerApp,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, valuesPath, files, commitMsg); err != nil {
		return err
	}

	summaryJSON, _ := json.Marshal(map[string]any{
		"name":        p.Name,
		"kind":        "S3Bucket",
		"bucket_name": p.BucketName,
		"region":      p.Region,
		"public":      p.Public,
		"app_ref":     p.AppRef,
		"status":      "Pending",
	})
	return db.UpsertSnapshot(ctx, w.pool,
		op.ProjectID, op.EnvironmentID,
		"S3Bucket", p.Name, "Pending", summaryJSON, time.Now(),
	)
}

func defaultIfEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
