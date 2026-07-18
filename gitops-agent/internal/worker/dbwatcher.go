package worker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/dada-tuda/console/gitops-agent/internal/crypto"
	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/mlflow"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
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
		if _, _, err := w.bootstrapProject(ctx, project, defaultMgr, nil); err != nil {
			return err
		}
	}

	return nil
}

// bootstrapProject renders and commits one project's project.yaml (idempotent —
// skipped if already in git) plus its Keycloak Group CRs (best-effort, logged
// not returned), and returns the project.yaml git path and commit sha for the
// caller to record against an operation. Shared by BootstrapProjects (agent
// startup, opID nil) and the CreateProject operation handler (opID set) so a
// project created at runtime gets the same manifest a restart would have given
// it — without needing a restart.
func (w *DBWatcher) bootstrapProject(ctx context.Context, project db.Project, defaultMgr *git.Manager, opID *uuid.UUID) (gitPath, sha string, err error) {
	mgr, err := w.managerFor(ctx, project.ID)
	if err != nil {
		return "", "", err
	}
	if err := mgr.EnsureCloned(); err != nil {
		return "", "", err
	}

	// Bootstrap project.yaml (idempotent — skip if already in git).
	gitPath = renderer.ProjectGitPath(project.Name)
	if _, err := mgr.ReadFile(gitPath); errors.Is(err, os.ErrNotExist) {
		yaml, err := renderer.RenderProject(renderer.ProjectSpec{
			Project:            project.Name,
			DisplayName:        project.DisplayName,
			OwnerType:          project.OwnerType,
			DefaultEnvironment: project.DefaultEnvironment,
			Quotas:             map[string]any{},
		})
		if err != nil {
			return "", "", err
		}
		commitMsg := fmt.Sprintf(
			"[DADA Console] Bootstrap project %s\n\nProject: %s\n",
			project.DisplayName, project.Name,
		)
		sha, err = mgr.CommitAndPush(gitPath, yaml, commitMsg, w.cfg.BotName, w.cfg.BotEmail)
		if err != nil {
			return "", "", err
		}
		if err := db.InsertCommit(ctx, w.pool,
			sha, mgr.RepoURL(), mgr.Branch(), gitPath, commitMsg,
			w.cfg.BotName, w.cfg.BotEmail, opID, "agent",
		); err != nil {
			log.Warn().Err(err).Str("project", project.Name).Msg("db-watcher: record bootstrap commit")
		}
		log.Info().Str("project", project.Name).Str("path", gitPath).Str("sha", sha).Msg("db-watcher: bootstrapped project manifest")
	} else if err != nil {
		return "", "", err
	} else {
		log.Debug().Str("project", project.Name).Str("path", gitPath).Msg("db-watcher: project already present in git")
	}

	// Bootstrap Keycloak Group CRs (idempotent — skip if already in git).
	// Requires default manager (argo-infra); skip gracefully if unavailable.
	if defaultMgr == nil {
		return gitPath, sha, nil
	}
	kcPath := renderer.ProjectGroupsGitPath(project.Name)
	if _, err := defaultMgr.ReadFile(kcPath); errors.Is(err, os.ErrNotExist) {
		members, err := db.ListProjectMembers(ctx, w.pool, project.Name)
		if err != nil {
			log.Warn().Err(err).Str("project", project.Name).Msg("db-watcher: list members for KC bootstrap")
			return gitPath, sha, nil
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
			return gitPath, sha, nil
		}
		commitMsg := fmt.Sprintf("[DADA Console] Bootstrap KC groups for project %s\n", project.Name)
		kcSha, err := defaultMgr.CommitAndPush(kcPath, kcYAML, commitMsg, w.cfg.BotName, w.cfg.BotEmail)
		if err != nil {
			log.Warn().Err(err).Str("project", project.Name).Msg("db-watcher: commit KC groups")
			return gitPath, sha, nil
		}
		log.Info().Str("project", project.Name).Str("path", kcPath).Str("sha", kcSha).Msg("db-watcher: bootstrapped KC group CRs")
	} else if err != nil {
		log.Warn().Err(err).Str("project", project.Name).Msg("db-watcher: check KC group file")
	} else {
		log.Debug().Str("project", project.Name).Str("path", kcPath).Msg("db-watcher: KC groups already in git")
	}

	return gitPath, sha, nil
}

// doCreateProject bootstraps the project.yaml (+ KC group CRs) for a project
// created at runtime (POST /projects or the default-project auto-provision),
// so nexus-cred and every other project-defaults resource land in its
// namespace without waiting for an agent restart. See BootstrapProjects for
// the same logic run over every project at startup.
func (w *DBWatcher) doCreateProject(ctx context.Context, op db.Operation) error {
	project, err := db.GetProjectByID(ctx, w.pool, op.ProjectID)
	if err != nil {
		return fmt.Errorf("project lookup: %w", err)
	}

	defaultMgr, ok := w.managers[w.cfg.DefaultRepoURL]
	if ok {
		if err := defaultMgr.EnsureCloned(); err != nil {
			log.Warn().Err(err).Msg("db-watcher: failed to clone default repo for KC group bootstrap")
			defaultMgr = nil
		}
	} else {
		defaultMgr = nil
	}

	opID := op.ID
	gitPath, sha, err := w.bootstrapProject(ctx, project, defaultMgr, &opID)
	if err != nil {
		return err
	}
	return db.MarkCommitted(ctx, w.pool, op.ID, sha, gitPath)
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
			w.cleanupFailedOptimisticSnapshot(ctx, op)
		}
	}
}

// cleanupFailedOptimisticSnapshot removes the Pending snapshot row the API seeds at
// create time when the operation later fails terminally: a database that never
// provisioned must disappear from the console instead of lingering as Pending, so
// readers only ever move between valid states. No-op for any other action.
func (w *DBWatcher) cleanupFailedOptimisticSnapshot(ctx context.Context, op db.Operation) {
	if op.Action != "CreateServiceDatabase" {
		return
	}
	if _, err := db.DeleteSnapshot(ctx, w.pool, op.ProjectID, op.EnvironmentID, "ServiceDatabaseV2", op.ResourceName); err != nil {
		log.Error().Err(err).Str("op", op.ID.String()).Msg("cleanup optimistic snapshot")
	}
}

func (w *DBWatcher) dispatch(ctx context.Context, op db.Operation) error {
	switch op.Action {
	case "CreateServiceDatabase":
		return w.doCreateServiceDatabase(ctx, op)
	case "DeleteServiceDatabase":
		return w.doDeleteServiceDatabase(ctx, op)
	case "CreateIngress":
		return w.doCreateIngress(ctx, op)
	case "CreateApp":
		return w.doCreateApp(ctx, op)
	case "DeleteApp":
		return w.doDeleteApp(ctx, op)
	case "MoveApp":
		return w.doMoveApp(ctx, op)
	case "CreateProject":
		return w.doCreateProject(ctx, op)
	case "DeleteProject":
		return w.doDeleteProject(ctx, op)
	case "DeployImageVersion":
		return w.doDeployImageVersion(ctx, op)
	case "UpdateAppStorage":
		return w.doUpdateAppStorage(ctx, op)
	case "CreatePublicApi":
		return w.doCreatePublicApi(ctx, op)
	case "AttachCustomHostname":
		return w.doAttachCustomHostname(ctx, op)
	case "AttachDefaultDomain":
		return w.doAttachDefaultDomain(ctx, op)
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
	case "AdoptComposeStack":
		return w.doAdoptComposeStack(ctx, op)
	case "RollbackStack":
		return w.doRollbackStack(ctx, op)
	default:
		return fmt.Errorf("unknown action: %s", op.Action)
	}
}

// doRollbackStack reverts a compose app's compose.yaml to its previous committed
// version and redeploys — the VM-runtime "Rollback" action (ADR-013 §8.3). Pure
// git: the previous file version becomes a new commit (auditable, forward-only),
// then a child DeployStack applies it. Data-safe by construction — the external
// PG volume pin lives in every version, so rolling the compose never touches data.
func (w *DBWatcher) doRollbackStack(ctx context.Context, op db.Operation) error {
	var p struct {
		AppName string `json:"app_name"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	if p.AppName == "" {
		return fmt.Errorf("rollback: app_name required")
	}

	projectName, envName, _, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}
	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}

	composePath := renderer.AppComposeGitPath(projectName, envName, p.AppName)
	prev, err := mgr.PreviousFileContent(composePath)
	if errors.Is(err, git.ErrNoPreviousVersion) {
		return fmt.Errorf("nothing to roll back: %q has only one committed version", p.AppName)
	}
	if err != nil {
		return fmt.Errorf("read previous compose version: %w", err)
	}

	commitMsg := fmt.Sprintf(
		"[DADA Console] Rollback compose app %s to previous version\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.AppName, op.ID, projectName, envName,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, composePath, []git.FileChange{
		{Path: composePath, Content: prev},
	}, commitMsg); err != nil {
		return err
	}

	deployID, err := db.EnqueueDeployStack(ctx, w.pool, op.ID, p.AppName)
	if err != nil {
		return fmt.Errorf("enqueue deploy stack: %w", err)
	}
	log.Info().Str("app", p.AppName).Str("deploy_op", deployID.String()).Msg("compose rollback committed; deploy enqueued")
	return nil
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
		ResourcesOnly:      true,
		ResourcesValueFile: renderer.AppResourcesValuesGitPath(projectName, envName, appName),
	})
	if err != nil {
		return nil, err
	}
	// Resources-carrier owner app: no workload of its own. Its App CR points
	// spec.helm.path at the shared helm/app-resources passthrough chart fed by the
	// app's resources.values.yaml (created lazily on the first Upsert; safely
	// absent until then via ignoreMissingValueFiles). No per-app resources/ chart
	// and no bare values.yaml are seeded: the former no longer exists on disk (ADR
	// 0005) and the latter is unused once valueFile points at resources.values.yaml.
	return []git.FileChange{
		{Path: appPath, Content: appYAML},
	}, nil
}

// doCreateComposeManagedDB materialises a managed database on a VM as a
// platform-owned first-class Application in the environment's aggregate stack: a
// pinned image with an external-pinned data volume. Its credentials (POSTGRES_*)
// were seeded into env_vars by the API handler, so renderEnvAggregate resolves
// them into the service's .env; a bound consumer app receives DATABASE_URL the
// same way. No public ports — apps reach it over the compose network by name.
func (w *DBWatcher) doCreateComposeManagedDB(ctx context.Context, op db.Operation, name, database, engine string) error {
	image, dataPath := "postgres:16", "/var/lib/postgresql/data"
	if engine == "redis" {
		image, dataPath = "redis:7", "/data"
	}
	volume := fmt.Sprintf("%s-data:%s", name, dataPath)
	summaryJSON := composeAppSummary(
		composeDesired{Image: image, Volumes: []string{volume}},
		map[string]any{"managed": engine, "database": database},
	)
	if err := db.UpsertSnapshot(ctx, w.pool,
		op.ProjectID, op.EnvironmentID, "App", name, "Pending", summaryJSON, time.Now(),
	); err != nil {
		return err
	}
	return w.renderEnvAggregate(ctx, op, op.ProjectID, op.EnvironmentID)
}

// doCreateIngress materialises a managed Ingress (routing + TLS) Resource on a VM
// env as a first-class nginx Application whose config is GENERATED from the
// routing spec (renderer.RenderNginxConf) and shipped from git — replacing
// hand-authored nginx templates. Then the env stack is reassembled. The nginx
// service shows as an ordinary first-class app (its own logs/metrics/state),
// marked managed=ingress.
//
// Config delivery (EDGE-safe): a throwaway probe proved git-relative bind mounts
// (`./apps/<name>/nginx.conf`) do NOT resolve on edge agents — the daemon creates
// an empty directory at the mount source (`cat: Is a directory`), same class as
// the earlier edge stack config-mount bug. So the generated conf ships
// base64-encoded in an env var (git delivers the compose file reliably) and the
// container's entrypoint decodes it to /etc/nginx/conf.d/default.conf before
// nginx boots. base64 has no compose-special chars ($ / quotes), so no
// interpolation escaping is needed. Certs/htpasswd stay host-absolute mounts
// (edge-safe; the live adopted nginx uses the same).
func (w *DBWatcher) doCreateIngress(ctx context.Context, op db.Operation) error {
	var p struct {
		Name        string   `json:"name"`
		Host        string   `json:"host"`
		Aliases     []string `json:"aliases"`
		SSLRedirect bool     `json:"ssl_redirect"`
		BasicAuth   string   `json:"basic_auth"`
		TLS         struct {
			Enabled    bool   `json:"enabled"`
			MinVersion string `json:"min_version"`
			CertPath   string `json:"cert_path"`
			KeyPath    string `json:"key_path"`
		} `json:"tls"`
		Rules []struct {
			Path string `json:"path"`
			App  string `json:"app"`
			Port int    `json:"port"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	if p.Name == "" || p.Host == "" {
		return fmt.Errorf("ingress: name and host are required")
	}

	spec := renderer.VMIngressSpec{
		Host: p.Host, Aliases: p.Aliases, SSLRedirect: p.SSLRedirect, BasicAuth: p.BasicAuth,
		TLS: renderer.VMIngressTLS{Enabled: p.TLS.Enabled, MinVersion: p.TLS.MinVersion, CertPath: p.TLS.CertPath, KeyPath: p.TLS.KeyPath},
	}
	depsSet := map[string]bool{}
	for _, r := range p.Rules {
		spec.Rules = append(spec.Rules, renderer.VMIngressRule{Path: r.Path, App: r.App, Port: r.Port})
		if r.App != "" {
			depsSet[r.App] = true
		}
	}
	deps := make([]string, 0, len(depsSet))
	for a := range depsSet {
		deps = append(deps, a)
	}
	sort.Strings(deps)
	block := ingressComposeBlock(spec, deps)

	// Structured Ingress spec persisted alongside the rendered compose so the
	// console renders routing/TLS as a first-class Resource (the generated conf is
	// base64-opaque; the console reads this, not the nginx.conf).
	ingressMeta := map[string]any{
		"host":         p.Host,
		"aliases":      p.Aliases,
		"ssl_redirect": p.SSLRedirect,
		"basic_auth":   p.BasicAuth != "",
		"tls": map[string]any{
			"enabled":     p.TLS.Enabled,
			"min_version": p.TLS.MinVersion,
			"cert_path":   p.TLS.CertPath,
		},
		"rules": p.Rules,
	}
	summaryJSON := composeAppSummary(
		composeDesired{Compose: block},
		map[string]any{"managed": "ingress", "host": p.Host, "ingress": ingressMeta},
	)
	if err := db.UpsertSnapshot(ctx, w.pool,
		op.ProjectID, op.EnvironmentID, "App", p.Name, "Pending", summaryJSON, time.Now(),
	); err != nil {
		return err
	}
	return w.renderEnvAggregate(ctx, op, op.ProjectID, op.EnvironmentID)
}

// ingressComposeBlock builds the nginx service block for a managed Ingress with
// EDGE-safe config delivery: the rendered conf is base64-encoded into an env var
// and decoded to disk by the entrypoint before nginx boots (git-relative bind
// mounts do NOT resolve on edge agents — see doCreateIngress). The entrypoint
// references the env var as $$NGINX_CONF_B64: compose interpolates a single $ at
// deploy time (against the host env, not the service env → empty), so the $$ is
// required to pass a literal $ through to the shell (both proven on the findata
// edge endpoint). Certs/htpasswd stay host-absolute mounts. deps must be sorted
// by the caller (deterministic output). Kept pure so the delivery contract is
// locked by a unit test.
func ingressComposeBlock(spec renderer.VMIngressSpec, deps []string) map[string]any {
	confB64 := base64.StdEncoding.EncodeToString([]byte(renderer.RenderNginxConf(spec)))
	vols := []string{}
	if spec.TLS.Enabled {
		vols = append(vols, "/etc/letsencrypt:/etc/nginx/certs:ro")
	}
	if spec.BasicAuth != "" {
		vols = append(vols, spec.BasicAuth+":"+spec.BasicAuth+":ro")
	}
	block := map[string]any{
		"image":       "nginx:1.27-alpine",
		"restart":     "unless-stopped",
		"ports":       []string{"80:80", "443:443"},
		"environment": map[string]any{"NGINX_CONF_B64": confB64},
		"entrypoint": []string{"/bin/sh", "-c",
			"echo \"$$NGINX_CONF_B64\" | base64 -d > /etc/nginx/conf.d/default.conf && exec nginx -g 'daemon off;'"},
	}
	if len(vols) > 0 {
		block["volumes"] = vols
	}
	if len(deps) > 0 {
		block["depends_on"] = deps
	}
	return block
}

func (w *DBWatcher) doCreateServiceDatabase(ctx context.Context, op db.Operation) error {
	var p struct {
		Name            string `json:"name"`
		Database        string `json:"database"`
		AppRef          string `json:"app_ref"`
		Engine          string `json:"engine"`
		BackupEnabled   bool   `json:"backup_enabled"`
		BackupSchedule  string `json:"backup_schedule"`
		BackupRetention string `json:"backup_retention"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}

	if p.Engine != "" {
		return w.doCreateComposeManagedDB(ctx, op, p.Name, p.Database, p.Engine)
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

// doDeleteServiceDatabase removes a managed ServiceDatabaseV2 CR entry from its
// owner app's resources.values.yaml and drops the snapshot; Argo prunes the CR
// once it leaves git. Mirrors doDeleteAIModel. AppRef comes from the operation
// payload (empty = the standalone "service-databases-<project>" owner app).
func (w *DBWatcher) doDeleteServiceDatabase(ctx context.Context, op db.Operation) error {
	var p struct {
		Name   string `json:"name"`
		AppRef string `json:"app_ref"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	if p.Name == "" {
		return fmt.Errorf("delete service database: name is required")
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
	valuesPath := renderer.ServiceDatabaseResourcesValuesGitPath(projectName, envName, p.AppRef)
	manifestFile, changed, err := removeManifestsFile(mgr, valuesPath, [][2]string{
		{"ServiceDatabaseV2", p.Name},
	})
	if err != nil {
		return fmt.Errorf("remove manifests: %w", err)
	}
	commitMsg := fmt.Sprintf(
		"[DADA Console] Delete ServiceDatabaseV2 %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.Name, op.ID, projectName, envName,
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
	if err := db.MarkCommitted(ctx, w.pool, op.ID, sha, valuesPath); err != nil {
		return err
	}
	_, _ = w.pool.Exec(ctx,
		`DELETE FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ServiceDatabaseV2' AND name = $3`,
		op.ProjectID, op.EnvironmentID, p.Name,
	)
	return nil
}

func (w *DBWatcher) doCreateApp(ctx context.Context, op db.Operation) error {
	var p struct {
		Name            string `json:"name"`
		Image           string `json:"image"`
		Framework       string `json:"framework"`
		Port            int    `json:"port"`
		Replicas        int    `json:"replicas"`
		Profile         string `json:"profile"`
		AppServerName   string `json:"app_server_name"`
		DefaultHostname string `json:"default_hostname"`
		WorkloadType    string `json:"workload_type"`
		Volume          *struct {
			Path         string `json:"path"`
			Size         string `json:"size"`
			StorageClass string `json:"storage_class"`
		} `json:"volume"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}

	// VM environments deploy as a Docker Compose stack (signalled by an AppServer
	// binding) rather than a Helm App.
	if p.AppServerName != "" {
		return w.doCreateComposeApp(ctx, op, p.Name)
	}

	if p.Port == 0 {
		p.Port = renderer.DefaultPortForFramework(p.Framework)
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
		Framework:          p.Framework,
		Port:               p.Port,
		Replicas:           p.Replicas,
		Profile:            p.Profile,
		OperationID:        op.ID.String(),
		HelmRepoURL:        mgr.RepoURL(),
		HelmTargetRevision: mgr.Branch(),
		Env:                env.Plain,
		WorkloadType:       p.WorkloadType,
	}
	if p.Volume != nil && p.Volume.Path != "" {
		appSpec.VolumePath = p.Volume.Path
		appSpec.VolumeSize = p.Volume.Size
		appSpec.VolumeStorageClass = p.Volume.StorageClass
	}
	if env.hasSecret() {
		appSpec.SecretEnvName = renderer.AppEnvSecretName(p.Name)
		for k := range env.Secret {
			appSpec.SecretEnvKeys = append(appSpec.SecretEnvKeys, k)
		}
	}
	argoName := renderer.ScopedArgoName(p.Name, envName, op.ProjectID.String())
	appSpec.ArgoName = argoName
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

	if p.DefaultHostname != "" {
		ingressYAML, iErr := renderer.RenderCustomIngress(renderer.CustomIngressSpec{
			Name:              renderer.FQDNToName(p.DefaultHostname),
			Namespace:         envNamespace,
			ProjectSlug:       projectName,
			EnvSlug:           envName,
			Hostname:          p.DefaultHostname,
			ServiceName:       renderer.AppServiceName(p.Name),
			ServicePortName:   renderer.DefaultAppServicePortName,
			OperationID:       op.ID.String(),
			WildcardTLSSecret: w.cfg.DefaultDomainTLSSecret,
			Managed:           true,
		})
		if iErr != nil {
			return iErr
		}
		dnsYAML, dErr := renderer.RenderDefaultDomainDNS(renderer.DefaultDomainDNSSpec{
			Name:        renderer.FQDNToName(p.DefaultHostname),
			ProjectSlug: projectName,
			EnvSlug:     envName,
			Hostname:    p.DefaultHostname,
			ServiceName: renderer.AppServiceName(p.Name),
			ServicePort: p.Port,
			Target:      w.cfg.DefaultDomainDNSTarget,
			OperationID: op.ID.String(),
		})
		if dErr != nil {
			return dErr
		}
		if err := mgr.EnsureCloned(); err != nil {
			return err
		}
		resValuesPath := renderer.AppResourcesValuesGitPath(projectName, envName, p.Name)
		manifestFile, mErr := upsertManifestsFile(mgr, resValuesPath, ingressYAML, dnsYAML)
		if mErr != nil {
			return mErr
		}
		files = append(files, manifestFile)
	}

	commitMsg := fmt.Sprintf(
		"[DADA Console] Create App %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.Name, op.ID, projectName, envName,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, gitPath, files, commitMsg); err != nil {
		return err
	}

	// Upsert snapshot so DeployImageVersion can re-render without reading git.
	summary := map[string]any{
		"image": p.Image, "framework": p.Framework, "port": p.Port, "replicas": p.Replicas,
		"profile": p.Profile, "status": "Pending", "argo_name": argoName,
	}
	if p.WorkloadType != "" {
		summary["workload_type"] = p.WorkloadType
	}
	if p.Volume != nil && p.Volume.Path != "" {
		summary["volume"] = map[string]any{
			"path": p.Volume.Path, "size": p.Volume.Size, "storage_class": p.Volume.StorageClass,
		}
	}
	summaryJSON, _ := json.Marshal(summary)
	return db.UpsertSnapshot(ctx, w.pool,
		op.ProjectID, op.EnvironmentID,
		"App", p.Name, "Pending", summaryJSON, time.Now(),
	)
}

// doDeleteApp removes an app's entire git folder in one commit: app.yaml,
// values.yaml, and resources.values.yaml. Removing app.yaml drops the app from
// the tenant-apps ApplicationSet generator, so ArgoCD tears the Application down
// (resources-finalizer). That finalizer must re-render helm/app to prune, but
// values.yaml is gone in the same commit, so it renders on chart defaults: the
// prune render only stays valid because helm/common now quotes the image
// (`image: ":"` on empty name/tag) instead of emitting a bare `image: :` that
// broke YAML parsing and wedged the Application in Terminating forever. Do NOT
// reintroduce an unquoted image in the chart. Missing files are skipped silently
// by RemoveAndPush. Also clears the app's own snapshot AND every child resource
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
		renderer.AppServiceGitPath(projectName, envName, p.Name),
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
	// reverse-sync stamps a top-level app_name; some CRs carry the link in
	// spec (spec.appRef / spec.attachedApp / spec.serviceName). Match any of
	// them, scoped to this project+env.
	_, _ = w.pool.Exec(ctx,
		`DELETE FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind <> 'App'
		   AND (
		        summary_json->>'app_ref'            = $3
		     OR summary_json->>'attached_app'       = $3
		     OR summary_json->>'app_name'           = $3
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

	// For VM (compose) apps, re-assemble the environment's aggregate stack without
	// the deleted app so its service leaves the running stack on the next deploy.
	// The snapshot is already gone, so renderEnvAggregate excludes it.
	var runtime string
	if err := w.pool.QueryRow(ctx, `SELECT runtime FROM environments WHERE id = $1`, op.EnvironmentID).Scan(&runtime); err == nil && runtime == "vm" {
		return w.renderEnvAggregate(ctx, op, op.ProjectID, op.EnvironmentID)
	}
	return nil
}

// doDeleteProject tears down an entire project (MVP scope): removes the whole
// git tree clusters/beget-prod/projects/<slug>/ in one commit so Argo prunes
// every app/resource the project owns, then wipes the project's DB rows in
// FK-safe order. The commit sha is captured and the operation marked committed
// BEFORE the DB wipe tx runs, because that tx deletes the operations row
// (including this op) as part of the cascade — MarkCommitted has already
// persisted by then. Namespace teardown and Keycloak cleanup are deliberately
// skipped: single-org collapse means there is no per-project Keycloak group,
// and Argo/git-prune plus namespace finalizers reap the namespace(s) without a
// worker-side k8s client.
func (w *DBWatcher) doDeleteProject(ctx context.Context, op db.Operation) error {
	var slug string
	if err := w.pool.QueryRow(ctx,
		`SELECT name FROM projects WHERE id = $1`, op.ProjectID,
	).Scan(&slug); err != nil {
		return fmt.Errorf("project lookup: %w", err)
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}
	if err := mgr.EnsureCloned(); err != nil {
		return err
	}

	projectDir := fmt.Sprintf("clusters/beget-prod/projects/%s", slug)
	commitMsg := fmt.Sprintf(
		"[DADA Console] Delete Project %s\n\nOperation: %s\n", slug, op.ID,
	)
	sha, err := mgr.RemoveAndPush([]string{projectDir}, commitMsg, w.cfg.BotName, w.cfg.BotEmail)
	if err != nil {
		return fmt.Errorf("git remove project tree: %w", err)
	}
	if sha != "" {
		opID := op.ID
		_ = db.InsertCommit(ctx, w.pool, sha, mgr.RepoURL(), mgr.Branch(),
			projectDir, commitMsg, w.cfg.BotName, w.cfg.BotEmail, &opID, "agent")
	}
	if err := db.MarkCommitted(ctx, w.pool, op.ID, sha, projectDir); err != nil {
		return err
	}

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin project wipe tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := wipeProjectRows(ctx, tx, op.ProjectID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit project wipe tx: %w", err)
	}

	log.Info().Str("project", slug).Str("project_id", op.ProjectID.String()).Msg("db-watcher: deleted project")
	return nil
}

// wipeProjectRows deletes every DB row a project owns, in FK-safe order, inside
// the caller's transaction. Tables that FK-reference project_id or
// environment_id with ON DELETE CASCADE are reaped by the final projects delete
// (environments cascade in turn); this function only handles the FKs Postgres
// will NOT cascade: git_commits.operation_id and environments.parent_env_id are
// detached, and the operations-referencing rows without ON DELETE CASCADE
// (deployments.operation_id, domain_hostnames.operation_id) plus the direct
// project/operation rows are deleted explicitly before the operations row they
// point at. Missing any operation_id child here re-triggers
// deployments_operation_id_fkey (SQLSTATE 23503) on the operations delete.
func wipeProjectRows(ctx context.Context, tx pgx.Tx, projectID uuid.UUID) error {
	if _, err := tx.Exec(ctx,
		`UPDATE git_commits SET operation_id = NULL WHERE operation_id IN (SELECT id FROM operations WHERE project_id = $1)`,
		projectID,
	); err != nil {
		return fmt.Errorf("detach git_commits: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE environments SET parent_env_id = NULL WHERE project_id = $1`,
		projectID,
	); err != nil {
		return fmt.Errorf("detach preview parent_env_id: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM audit_events WHERE project_id = $1`, projectID); err != nil {
		return fmt.Errorf("delete audit_events: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM db_backups WHERE project_id = $1`, projectID); err != nil {
		return fmt.Errorf("delete db_backups: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM resource_snapshots WHERE project_id = $1`, projectID); err != nil {
		return fmt.Errorf("delete resource_snapshots: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM deployments WHERE environment_id IN (SELECT id FROM environments WHERE project_id = $1)`,
		projectID,
	); err != nil {
		return fmt.Errorf("delete deployments: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM domain_hostnames WHERE environment_id IN (SELECT id FROM environments WHERE project_id = $1)`,
		projectID,
	); err != nil {
		return fmt.Errorf("delete domain_hostnames: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM operations WHERE project_id = $1`, projectID); err != nil {
		return fmt.Errorf("delete operations: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM projects WHERE id = $1`, projectID); err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return nil
}

// composeDesired is one Application's durable desired compose service spec,
// stored under resource_snapshots.summary_json.desired at create/import time.
// renderEnvAggregate reads it back to reassemble the per-environment stack; the
// portainer-agent live-status sync merges (never overwrites) summary_json, so
// this key survives deploy reconciliation.
type composeDesired struct {
	Image   string   `json:"image"`
	Ports   []string `json:"ports,omitempty"`
	Volumes []string `json:"volumes,omitempty"`
	// Compose, when set, is the VERBATIM compose service block for an ADOPTED app
	// (a hand-authored prod stack migrated into per-service Applications). It
	// takes precedence over Image/Ports/Volumes so the aggregate reproduces the
	// live service exactly (environment/expose/depends_on/bind-mounts preserved).
	// StackVolumes is the stack's original top-level volumes block, carried so the
	// external-volume name mapping (e.g. profi_pg_data -> compose_profi_pg_data)
	// is never lost — the data-safety invariant for an adopted stateful stack.
	Compose      map[string]any `json:"compose,omitempty"`
	StackVolumes map[string]any `json:"stack_volumes,omitempty"`
	// Files are extra files written into the app's git dir alongside service.yaml
	// (keyed by path relative to that dir), e.g. a managed Ingress's generated
	// nginx.conf. The app's Compose block mounts them (relative to the env-level
	// aggregate compose, i.e. ./apps/<name>/<file>). Lets a Resource ship rendered
	// config declaratively from git instead of a hand-authored host file.
	Files map[string]string `json:"files,omitempty"`
}

// composeAppSummary marks an App snapshot as a first-class VM (compose)
// Application and carries its desired service spec.
func composeAppSummary(desired composeDesired, extra map[string]any) json.RawMessage {
	m := map[string]any{"runtime": "compose", "status": "Pending", "desired": desired}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return b
}

// renderEnvAggregate is the AppServer assembly step: it renders EVERY first-class
// Application in the environment (each a resource_snapshots kind=App with a
// desired compose spec) into one per-app service.yaml fragment + per-app .env,
// plus the single aggregate compose.yaml the environment's Portainer endpoint
// deploys as ONE stack ({projectSlug}-{envSlug}). It reads desired specs from
// the DB (summary_json.desired), so it needs no git read-back; env vars are
// resolved per app from the env_vars table. Apps with no image yet are skipped
// from the stack (no workload until configured, like the k8s bare-app path).
// Callers MUST upsert their App snapshot (with desired) BEFORE invoking this so
// the new/updated app is included.
func (w *DBWatcher) renderEnvAggregate(ctx context.Context, op db.Operation, projectID uuid.UUID, environmentID *uuid.UUID) error {
	projectName, envName, _, err := w.projectEnv(ctx, projectID, environmentID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}
	mgr, err := w.managerFor(ctx, projectID)
	if err != nil {
		return err
	}

	rows, err := w.pool.Query(ctx,
		`SELECT name, summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App'
		 ORDER BY name`,
		projectID, environmentID,
	)
	if err != nil {
		return fmt.Errorf("list env apps: %w", err)
	}
	type appRow struct {
		name    string
		summary []byte
	}
	var apps []appRow
	for rows.Next() {
		var a appRow
		if err := rows.Scan(&a.name, &a.summary); err != nil {
			rows.Close()
			return fmt.Errorf("scan app snapshot: %w", err)
		}
		apps = append(apps, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var specs []renderer.AppServiceSpec
	var files []git.FileChange
	var stackVolumes map[string]any
	for _, a := range apps {
		var s struct {
			Desired composeDesired `json:"desired"`
		}
		_ = json.Unmarshal(a.summary, &s)
		adopted := s.Desired.Compose != nil
		if s.Desired.Image == "" && !adopted {
			continue
		}
		env, err := w.resolveRuntimeEnv(ctx, environmentID, a.name)
		if err != nil {
			return err
		}
		merged := env.merged()
		spec := renderer.AppServiceSpec{
			AppName: a.name,
			Image:   s.Desired.Image,
			Ports:   s.Desired.Ports,
			Volumes: s.Desired.Volumes,
			HasEnv:  len(merged) > 0,
			Service: s.Desired.Compose,
		}
		if s.Desired.StackVolumes != nil {
			stackVolumes = s.Desired.StackVolumes
		}
		frag, err := renderer.RenderAppServiceFragment(spec)
		if err != nil {
			return err
		}
		specs = append(specs, spec)
		files = append(files,
			git.FileChange{Path: renderer.AppServiceGitPath(projectName, envName, a.name), Content: frag},
			git.FileChange{Path: renderer.AppEnvGitPath(projectName, envName, a.name), Content: renderer.RenderEnvFile(merged)},
		)
		// Extra rendered config files this app ships (e.g. a managed Ingress's
		// generated nginx.conf), written into the app's git dir.
		for rel, content := range s.Desired.Files {
			files = append(files, git.FileChange{
				Path:    renderer.AppBaseGitPath(projectName, envName, a.name) + "/" + rel,
				Content: content,
			})
		}
	}

	aggPath := renderer.EnvComposeGitPath(projectName, envName)
	agg, err := renderer.RenderAggregateCompose(specs, stackVolumes)
	if err != nil {
		return err
	}
	files = append(files, git.FileChange{Path: aggPath, Content: agg})

	commitMsg := fmt.Sprintf(
		"[DADA Console] Assemble compose stack for %s/%s (%d apps)\n\nOperation: %s\n",
		projectName, envName, len(specs), op.ID,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, aggPath, files, commitMsg); err != nil {
		return err
	}

	envStack := projectName + "-" + envName
	deployID, err := db.EnqueueDeployStack(ctx, w.pool, op.ID, envStack)
	if err != nil {
		return fmt.Errorf("enqueue deploy stack: %w", err)
	}
	log.Info().Str("env_stack", envStack).Int("apps", len(specs)).Str("deploy_op", deployID.String()).
		Msg("assembled compose stack; deploy enqueued")
	return nil
}

// doAdoptComposeStack converts an existing hand-authored single compose app
// (source_app, e.g. an adopted VM's `profi-vm`) into N first-class per-service
// Applications WITHOUT losing fidelity: it reads the live compose from git,
// splits it into one Application per service carrying that service's VERBATIM
// block, preserves the stack's top-level volumes (external-name mapping intact —
// the data-safety invariant), copies the stack .env to the environment level,
// renders the aggregate (== the live stack), replaces the single-app snapshot
// with the N per-service snapshots, and enqueues a deploy of the assembled stack.
// The external volume survives the stack swap, so prod data is preserved even
// though the containers are recreated (a brief, acceptable cutover outage).
// This is the reusable "adopt an existing compose" path; findata is its first use.
func (w *DBWatcher) doAdoptComposeStack(ctx context.Context, op db.Operation) error {
	var p struct {
		SourceApp string `json:"source_app"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	if p.SourceApp == "" {
		return fmt.Errorf("adopt: source_app is required")
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

	composeRaw, err := mgr.ReadFile(renderer.AppComposeGitPath(projectName, envName, p.SourceApp))
	if err != nil {
		return fmt.Errorf("read source compose: %w", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(composeRaw), &doc); err != nil {
		return fmt.Errorf("parse source compose: %w", err)
	}
	servicesRaw, _ := doc["services"].(map[string]any)
	if len(servicesRaw) == 0 {
		return fmt.Errorf("adopt: source compose %q has no services", p.SourceApp)
	}
	volumes, _ := doc["volumes"].(map[string]any)
	sourceEnv, _ := mgr.ReadFile(renderer.AppEnvGitPath(projectName, envName, p.SourceApp))

	names := make([]string, 0, len(servicesRaw))
	for name := range servicesRaw {
		names = append(names, name)
	}
	sort.Strings(names)

	var specs []renderer.AppServiceSpec
	var files []git.FileChange
	for _, name := range names {
		block, _ := servicesRaw[name].(map[string]any)
		spec := renderer.AppServiceSpec{AppName: name, Service: block}
		specs = append(specs, spec)
		frag, err := renderer.RenderAppServiceFragment(spec)
		if err != nil {
			return err
		}
		files = append(files, git.FileChange{Path: renderer.AppServiceGitPath(projectName, envName, name), Content: frag})

		desired := map[string]any{"compose": block}
		if volumes != nil {
			desired["stack_volumes"] = volumes
		}
		if img, ok := block["image"].(string); ok {
			desired["image"] = img
		}
		summaryJSON, _ := json.Marshal(map[string]any{
			"runtime": "compose", "status": "Pending", "desired": desired, "adopted_from": p.SourceApp,
		})
		if err := db.UpsertSnapshot(ctx, w.pool,
			op.ProjectID, op.EnvironmentID, "App", name, "Pending", summaryJSON, time.Now(),
		); err != nil {
			return err
		}
	}

	agg, err := renderer.RenderAggregateCompose(specs, volumes)
	if err != nil {
		return err
	}
	aggPath := renderer.EnvComposeGitPath(projectName, envName)
	files = append(files,
		git.FileChange{Path: aggPath, Content: agg},
		git.FileChange{Path: renderer.EnvDotEnvGitPath(projectName, envName), Content: sourceEnv},
	)

	commitMsg := fmt.Sprintf(
		"[DADA Console] Adopt compose stack %s → %d Applications for %s/%s\n\nOperation: %s\n",
		p.SourceApp, len(specs), projectName, envName, op.ID,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, aggPath, files, commitMsg); err != nil {
		return err
	}

	_, _ = w.pool.Exec(ctx,
		`DELETE FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		op.ProjectID, op.EnvironmentID, p.SourceApp,
	)

	envStack := projectName + "-" + envName
	deployID, err := db.EnqueueDeployStack(ctx, w.pool, op.ID, envStack)
	if err != nil {
		return fmt.Errorf("enqueue deploy stack: %w", err)
	}
	log.Info().Str("source", p.SourceApp).Int("apps", len(specs)).Str("env_stack", envStack).
		Str("deploy_op", deployID.String()).Msg("adopted compose stack into per-service apps; deploy enqueued")
	return nil
}

// doCreateComposeApp records a first-class VM Application (its desired compose
// service spec) and re-assembles the environment's aggregate stack. Supersedes
// the former per-app compose.yaml skeleton: the app is now one service in the
// shared per-environment stack (renderer.EnvComposeGitPath), not its own stack.
func (w *DBWatcher) doCreateComposeApp(ctx context.Context, op db.Operation, appName string) error {
	var p struct {
		Image string `json:"image"`
		Port  int    `json:"port"`
	}
	_ = json.Unmarshal(op.Payload, &p)

	var ports []string
	if p.Port > 0 {
		ports = []string{fmt.Sprintf("%d:%d", p.Port, p.Port)}
	}
	summaryJSON := composeAppSummary(composeDesired{Image: p.Image, Ports: ports}, nil)
	if err := db.UpsertSnapshot(ctx, w.pool,
		op.ProjectID, op.EnvironmentID,
		"App", appName, "Pending", summaryJSON, time.Now(),
	); err != nil {
		return err
	}
	return w.renderEnvAggregate(ctx, op, op.ProjectID, op.EnvironmentID)
}

// doImportComposeStack adopts a discovered VM workload (DiscoverWorkload) into
// N first-class Applications — one per included service, NOT one opaque stack —
// each recorded as a kind=App snapshot carrying its desired compose spec
// (image/ports/volumes, with named volumes pinned external by the aggregate
// renderer so the first deploy attaches existing prod data). It then runs the
// AppServer assembly (renderEnvAggregate) once to render all apps into the
// shared per-environment compose.yaml and deploy them as one stack, so every
// imported service shows up as an ordinary managed app with per-app
// state/logs/metrics. Per-app env vars are seeded into the env_vars table by the
// API handler (ImportComposeStack), so resolveRuntimeEnv here reflects them and
// a later "edit env" through the normal app UI works.
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

	for _, svc := range p.Services {
		if !svc.Include {
			continue
		}
		summaryJSON := composeAppSummary(
			composeDesired{Image: svc.Image, Ports: svc.Ports, Volumes: svc.Volumes},
			map[string]any{"imported_from": p.ServerName},
		)
		if err := db.UpsertSnapshot(ctx, w.pool,
			op.ProjectID, op.EnvironmentID,
			"App", svc.ServiceName, "Pending", summaryJSON, time.Now(),
		); err != nil {
			return err
		}
	}

	return w.renderEnvAggregate(ctx, op, op.ProjectID, op.EnvironmentID)
}

// updateComposeAppImage points a VM (compose) Application at a new image by
// patching its desired.image in the snapshot and re-assembling the environment's
// aggregate stack. Mirrors the k8s image-update path but for the compose runtime,
// where the workload lives as one service in the shared per-VM stack.
func (w *DBWatcher) updateComposeAppImage(ctx context.Context, op db.Operation, appName, image string) error {
	var summaryRaw []byte
	if err := w.pool.QueryRow(ctx, `
		SELECT summary_json FROM resource_snapshots
		WHERE project_id=$1 AND environment_id=$2 AND kind='App' AND name=$3
	`, op.ProjectID, op.EnvironmentID, appName).Scan(&summaryRaw); err != nil {
		return fmt.Errorf("loading app snapshot: %w", err)
	}
	cur := map[string]any{}
	_ = json.Unmarshal(summaryRaw, &cur)
	desired, _ := cur["desired"].(map[string]any)
	if desired == nil {
		desired = map[string]any{}
	}
	desired["image"] = image
	cur["desired"] = desired
	cur["runtime"] = "compose"
	cur["status"] = "Pending"
	updatedJSON, _ := json.Marshal(cur)
	if err := db.UpsertSnapshot(ctx, w.pool,
		op.ProjectID, op.EnvironmentID, "App", appName, "Pending", updatedJSON, time.Now(),
	); err != nil {
		return err
	}
	return w.renderEnvAggregate(ctx, op, op.ProjectID, op.EnvironmentID)
}

func (w *DBWatcher) doDeployImageVersion(ctx context.Context, op db.Operation) error {
	var p struct {
		AppName   string `json:"app_name"`
		Image     string `json:"image"`
		Framework string `json:"framework"`
		Port      int    `json:"port"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}

	var runtime string
	if err := w.pool.QueryRow(ctx, `SELECT runtime FROM environments WHERE id = $1`, op.EnvironmentID).Scan(&runtime); err != nil {
		return fmt.Errorf("load env runtime: %w", err)
	}
	if runtime == "vm" {
		return w.updateComposeAppImage(ctx, op, p.AppName, p.Image)
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
	frameworkVal, _ := cur["framework"].(string)
	if p.Framework != "" {
		frameworkVal = p.Framework
	}
	if p.Port > 0 {
		portVal = float64(p.Port)
	}
	if portVal == 0 {
		portVal = float64(renderer.DefaultPortForFramework(frameworkVal))
	}
	if replicasVal == 0 {
		replicasVal = 1
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
		Framework:          frameworkVal,
		Port:               int(portVal),
		Replicas:           int(replicasVal),
		Profile:            profileVal,
		OperationID:        op.ID.String(),
		HelmRepoURL:        mgr.RepoURL(),
		HelmTargetRevision: mgr.Branch(),
		Env:                env.Plain,
	}
	if vp, vs, vsc := volumeFromSummary(cur); vp != "" {
		appSpec.VolumePath = vp
		appSpec.VolumeSize = vs
		appSpec.VolumeStorageClass = vsc
	}
	appSpec.ArgoName, _ = cur["argo_name"].(string)
	appSpec.WorkloadType, _ = cur["workload_type"].(string)
	if env.hasSecret() {
		appSpec.SecretEnvName = renderer.AppEnvSecretName(p.AppName)
		for k := range env.Secret {
			appSpec.SecretEnvKeys = append(appSpec.SecretEnvKeys, k)
		}
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
	cur["framework"] = frameworkVal
	cur["port"] = portVal
	cur["status"] = "Pending"
	updatedJSON, _ := json.Marshal(cur)
	return db.UpsertSnapshot(ctx, w.pool,
		op.ProjectID, op.EnvironmentID,
		"App", p.AppName, "Pending", updatedJSON, time.Now(),
	)
}

// volumeFromSummary extracts a persistent-directory spec from a resource_snapshot
// summary_json map. It returns empty strings when no volume is configured.
func volumeFromSummary(cur map[string]any) (path, size, storageClass string) {
	v, ok := cur["volume"].(map[string]any)
	if !ok {
		return "", "", ""
	}
	path, _ = v["path"].(string)
	size, _ = v["size"].(string)
	storageClass, _ = v["storage_class"].(string)
	return path, size, storageClass
}

func workloadTypeFromSummary(cur map[string]any) string {
	wt, _ := cur["workload_type"].(string)
	return wt
}

// doUpdateAppStorage attaches or resizes an app's persistent data directory. It
// re-renders app.yaml + values.yaml from the current snapshot (image/port/
// replicas/profile) with the new common.pvc block, then records the volume in the
// snapshot so subsequent deploys keep it. The workload chart maps the block to a
// ReadWriteMany PVC; resizes rely on the storage class allowing volume expansion.
func (w *DBWatcher) doUpdateAppStorage(ctx context.Context, op db.Operation) error {
	var p struct {
		AppName string `json:"app_name"`
		Volume  struct {
			Path         string `json:"path"`
			Size         string `json:"size"`
			StorageClass string `json:"storage_class"`
		} `json:"volume"`
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

	imageVal, _ := cur["image"].(string)
	portVal, _ := cur["port"].(float64)
	replicasVal, _ := cur["replicas"].(float64)
	profileVal, _ := cur["profile"].(string)
	frameworkVal, _ := cur["framework"].(string)
	if portVal == 0 {
		portVal = float64(renderer.DefaultPortForFramework(frameworkVal))
	}
	if replicasVal == 0 {
		replicasVal = 1
	}
	if profileVal == "" {
		profileVal = "small"
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}

	env, err := w.resolveRuntimeEnv(ctx, op.EnvironmentID, p.AppName)
	if err != nil {
		return err
	}

	appSpec := renderer.AppSpec{
		Name:               p.AppName,
		Namespace:          envNamespace,
		ProjectSlug:        projectName,
		EnvSlug:            envName,
		Image:              imageVal,
		Framework:          frameworkVal,
		Port:               int(portVal),
		Replicas:           int(replicasVal),
		Profile:            profileVal,
		OperationID:        op.ID.String(),
		HelmRepoURL:        mgr.RepoURL(),
		HelmTargetRevision: mgr.Branch(),
		Env:                env.Plain,
		WorkloadType:       workloadTypeFromSummary(cur),
		VolumePath:         p.Volume.Path,
		VolumeSize:         p.Volume.Size,
		VolumeStorageClass: p.Volume.StorageClass,
	}
	appSpec.ArgoName, _ = cur["argo_name"].(string)
	if env.hasSecret() {
		appSpec.SecretEnvName = renderer.AppEnvSecretName(p.AppName)
		for k := range env.Secret {
			appSpec.SecretEnvKeys = append(appSpec.SecretEnvKeys, k)
		}
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
		"[DADA Console] Update storage for app %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.AppName, op.ID, projectName, envName,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, gitPath, files, commitMsg); err != nil {
		return err
	}

	cur["volume"] = map[string]any{
		"path": p.Volume.Path, "size": p.Volume.Size, "storage_class": p.Volume.StorageClass,
	}
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

// doAttachDefaultDomain backfills a managed surrogate domain onto an existing
// app. It renders the same two manifests as doCreateApp's default-domain block —
// a per-host managed Ingress (WildcardTLSSecret when configured, else per-host
// HTTP-01) plus a DNS-only PublicApi that publishes the A record into the Beget
// zone — into the app's resources.values.yaml. The managed domain_hostnames row
// is inserted by the enqueuer.
func (w *DBWatcher) doAttachDefaultDomain(ctx context.Context, op db.Operation) error {
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
	frameworkVal, _ := cur["framework"].(string)
	if portVal == 0 {
		portVal = float64(renderer.DefaultPortForFramework(frameworkVal))
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}

	ingressYAML, err := renderer.RenderCustomIngress(renderer.CustomIngressSpec{
		Name:              renderer.FQDNToName(p.Hostname),
		Namespace:         envNamespace,
		ProjectSlug:       projectName,
		EnvSlug:           envName,
		Hostname:          p.Hostname,
		ServiceName:       renderer.AppServiceName(p.AppName),
		ServicePortName:   renderer.DefaultAppServicePortName,
		OperationID:       op.ID.String(),
		WildcardTLSSecret: w.cfg.DefaultDomainTLSSecret,
		Managed:           true,
	})
	if err != nil {
		return err
	}
	dnsYAML, err := renderer.RenderDefaultDomainDNS(renderer.DefaultDomainDNSSpec{
		Name:        renderer.FQDNToName(p.Hostname),
		ProjectSlug: projectName,
		EnvSlug:     envName,
		Hostname:    p.Hostname,
		ServiceName: renderer.AppServiceName(p.AppName),
		ServicePort: int(portVal),
		Target:      w.cfg.DefaultDomainDNSTarget,
		OperationID: op.ID.String(),
	})
	if err != nil {
		return err
	}

	appFiles, err := w.ensureAppExists(mgr, projectName, envName, p.AppName, envNamespace, op.ID.String())
	if err != nil {
		return err
	}
	valuesPath := renderer.AppResourcesValuesGitPath(projectName, envName, p.AppName)
	manifestFile, err := upsertManifestsFile(mgr, valuesPath, ingressYAML, dnsYAML)
	if err != nil {
		return err
	}
	files := append(appFiles, manifestFile)

	commitMsg := fmt.Sprintf(
		"[DADA Console] Attach default domain %s to app %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
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
