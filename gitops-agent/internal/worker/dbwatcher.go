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
func (w *DBWatcher) BootstrapProjects(ctx context.Context) error {
	projects, err := db.ListProjects(ctx, w.pool)
	if err != nil {
		return err
	}

	for _, project := range projects {
		mgr, err := w.managerFor(ctx, project.ID)
		if err != nil {
			return err
		}
		if err := mgr.EnsureCloned(); err != nil {
			return err
		}

		gitPath := renderer.ProjectGitPath(project.Name)
		if _, err := mgr.ReadFile(gitPath); err == nil {
			log.Debug().Str("project", project.Name).Str("path", gitPath).Msg("db-watcher: project already present in git")
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}

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
	case "DeployImageVersion":
		return w.doDeployImageVersion(ctx, op)
	case "CreatePublicApi":
		return w.doCreatePublicApi(ctx, op)
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
	if err != nil || integration == nil {
		// Use default manager
		return w.managers[w.cfg.DefaultRepoURL], nil
	}

	if mgr, ok := w.managers[integration.RepoURL]; ok {
		return mgr, nil
	}

	token, err := crypto.DecryptToken(w.cfg.EncryptionKey, integration.TokenEncrypted)
	if err != nil {
		log.Warn().Err(err).Str("project", projectID.String()).Msg("could not decrypt token, falling back to default repo")
		return w.managers[w.cfg.DefaultRepoURL], nil
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
// application.yaml + values.yaml when they are not yet present in git. When the
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
	return []git.FileChange{
		{Path: appPath, Content: appYAML},
		{Path: renderer.AppHelmValuesGitPath(projectName, envName, appName), Content: renderer.RenderBareAppValues()},
		{Path: renderer.AppResourcesChartYamlGitPath(projectName, envName, appName), Content: renderer.RenderChartYaml(appName)},
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

	gitPath := renderer.ServiceDatabaseGitPath(projectName, envName, p.AppRef)
	files := []git.FileChange{{Path: gitPath, Content: yaml}}
	appFiles, err := w.ensureAppExists(mgr, projectName, envName, p.AppRef, envNamespace, op.ID.String())
	if err != nil {
		return err
	}
	files = append(files, appFiles...)

	commitMsg := fmt.Sprintf(
		"[DADA Console] Create ServiceDatabaseV2 %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.Name, op.ID, projectName, envName,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, gitPath, files, commitMsg); err != nil {
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
			"appRef":   p.AppRef,
			"namespace": envNamespace,
			"database": p.Database,
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

	appSpec := renderer.AppSpec{
		Name:        p.Name,
		Namespace:   envNamespace,
		ProjectSlug: projectName,
		EnvSlug:     envName,
		Image:       p.Image,
		Port:        p.Port,
		Replicas:    p.Replicas,
		Profile:     p.Profile,
		OperationID: op.ID.String(),
		HelmRepoURL:        mgr.RepoURL(),
		HelmTargetRevision: mgr.Branch(),
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
	commitMsg := fmt.Sprintf(
		"[DADA Console] Create App %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.Name, op.ID, projectName, envName,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, gitPath, []git.FileChange{
		{Path: gitPath, Content: yaml},
		{Path: valuesPath, Content: valuesYAML},
		{Path: renderer.AppResourcesChartYamlGitPath(projectName, envName, p.Name), Content: renderer.RenderChartYaml(p.Name)},
	}, commitMsg); err != nil {
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

	composePath := renderer.AppComposeGitPath(projectName, envName, appName)
	envPath := renderer.AppEnvGitPath(projectName, envName, appName)
	commitMsg := fmt.Sprintf(
		"[DADA Console] Create compose app %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		appName, op.ID, projectName, envName,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, composePath, []git.FileChange{
		{Path: composePath, Content: renderer.RenderComposeSkeleton(appName)},
		{Path: envPath, Content: renderer.RenderEnvSkeleton()},
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

	appSpec := renderer.AppSpec{
		Name:        p.AppName,
		Namespace:   envNamespace,
		ProjectSlug: projectName,
		EnvSlug:     envName,
		Image:       p.Image,
		Port:        int(portVal),
		Replicas:    int(replicasVal),
		Profile:     profileVal,
		OperationID: op.ID.String(),
		HelmRepoURL:        mgr.RepoURL(),
		HelmTargetRevision: mgr.Branch(),
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
	commitMsg := fmt.Sprintf(
		"[DADA Console] Deploy image %s for app %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.Image, p.AppName, op.ID, projectName, envName,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, gitPath, []git.FileChange{
		{Path: gitPath, Content: yaml},
		{Path: valuesPath, Content: valuesYAML},
	}, commitMsg); err != nil {
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

	gitPath := renderer.PublicApiGitPath(projectName, envName, p.AppName, p.PublicApiName)
	files := []git.FileChange{{Path: gitPath, Content: yaml}}
	appFiles, err := w.ensureAppExists(mgr, projectName, envName, p.AppName, envNamespace, op.ID.String())
	if err != nil {
		return err
	}
	files = append(files, appFiles...)

	commitMsg := fmt.Sprintf(
		"[DADA Console] Register domain %s for app %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.FQDN, p.AppName, op.ID, projectName, envName,
	)
	return w.commitFilesAndRecord(ctx, op, mgr, gitPath, files, commitMsg)
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

func defaultIfEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
