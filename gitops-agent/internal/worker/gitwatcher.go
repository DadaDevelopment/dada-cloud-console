package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/dada-tuda/console/gitops-agent/internal/crypto"
	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// appPathRe matches clusters/<cluster>/projects/<project>/environments/<env>/apps/<app>/app.yaml
// Capture groups: 1=project, 2=env, 3=app
var appPathRe = regexp.MustCompile(`^clusters/[^/]+/projects/([^/]+)/environments/([^/]+)/apps/([^/]+)/app\.yaml$`)

// valuesPathRe matches clusters/<cluster>/projects/<project>/environments/<env>/apps/<app>/values.yaml
// Capture groups: 1=project, 2=env, 3=app
var valuesPathRe = regexp.MustCompile(`^clusters/[^/]+/projects/([^/]+)/environments/([^/]+)/apps/([^/]+)/values\.yaml$`)

// projectPathRe matches clusters/<cluster>/projects/<project>/project.yaml.
// Capture group 1 is the project slug.
var projectPathRe = regexp.MustCompile(`^clusters/[^/]+/projects/([^/]+)/project\.yaml$`)

// namespacePolicyPathRe matches clusters/<cluster>/namespace-policies/<namespace>.yaml.
// Capture group 1 is the k8s namespace name.
var namespacePolicyPathRe = regexp.MustCompile(`^clusters/[^/]+/namespace-policies/([^/]+)\.yaml$`)

// resourcesValuesPathRe matches an app's resources.values.yaml (ADR 0005):
// clusters/<cluster>/projects/<project>/environments/<env>/apps/<app>/resources.values.yaml
// This single file holds every child CR (ServiceDatabase, AIModel, PublicApi,
// S3Bucket, ...) as entries in a top-level manifests: list. Capture groups:
// 1=project, 2=env, 3=app. Supersedes the former resources/templates/<kind>.yaml
// layout.
var resourcesValuesPathRe = regexp.MustCompile(`^clusters/[^/]+/projects/([^/]+)/environments/([^/]+)/apps/([^/]+)/resources\.values\.yaml$`)

// chartTemplatePathRe matches a platform CR delivered by an app's helm chart
// rather than the resources.values.yaml passthrough:
// clusters/<cluster>/projects/<project>/environments/<env>/apps/<app>/chart/templates/<file>.yaml
// Some platform apps (e.g. mimir, opensearch) provision their S3Bucket via a
// chart template; indexing these too keeps the console's resource view in sync
// with git regardless of which delivery style an app uses. Capture groups:
// 1=project, 2=env, 3=app.
var chartTemplatePathRe = regexp.MustCompile(`^clusters/[^/]+/projects/([^/]+)/environments/([^/]+)/apps/([^/]+)/chart/templates/[^/]+\.ya?ml$`)

// ValuesNotifier is implemented by server.Hub to push live file updates to WS clients.
type ValuesNotifier interface {
	Notify(project, env, app, file, yaml string)
}

type namespacePolicyManifest struct {
	Namespace     string         `yaml:"namespace"`
	LimitRange    map[string]any `yaml:"limitRange"`
	ResourceQuota map[string]any `yaml:"resourceQuota"`
}

// resourceManifest extracts the GVK name from any namespaced CR manifest so a
// reverse-synced resources/templates manifest lands in resource_snapshots under
// the kind the read APIs expect (ServiceDatabase / AIModel / PublicApi).
type resourceManifest struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec map[string]any `yaml:"spec"`
}

type envManifest struct {
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
	Type      string `yaml:"type"`
}

type projectManifest struct {
	Project            string         `yaml:"project"`
	DisplayName        string         `yaml:"displayName"`
	OwnerType          string         `yaml:"ownerType"`
	DefaultEnvironment string         `yaml:"defaultEnvironment"`
	Environments       []envManifest  `yaml:"environments"`
	Quotas             map[string]any `yaml:"quotas"`
}

// GitWatcher polls remote repos for new commits and syncs them to the DB.
type GitWatcher struct {
	pool     *pgxpool.Pool
	cfg      *config.Config
	managers map[string]*git.Manager
	// notifier receives live values.yaml updates (may be nil — disabled when no WS server).
	notifier ValuesNotifier
}

func NewGitWatcher(pool *pgxpool.Pool, cfg *config.Config, defaultMgr *git.Manager) *GitWatcher {
	return &GitWatcher{
		pool: pool,
		cfg:  cfg,
		managers: map[string]*git.Manager{
			cfg.DefaultRepoURL: defaultMgr,
		},
	}
}

// WithValuesNotifier attaches a hub so changes to values.yaml are pushed to
// any open WS editor sessions after each git pull.
func (w *GitWatcher) WithValuesNotifier(n ValuesNotifier) {
	w.notifier = n
}

func (w *GitWatcher) Start(ctx context.Context) {
	log.Info().Dur("interval", w.cfg.PollIntervalGit).Msg("git-watcher started")
	ticker := time.NewTicker(w.cfg.PollIntervalGit)
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

// TriggerNow allows the webhook handler to request an immediate sync.
func (w *GitWatcher) TriggerNow(ctx context.Context) {
	w.poll(ctx)
}

func (w *GitWatcher) poll(ctx context.Context) {
	// Build list of managers to poll: default + any project integrations.
	managers := w.currentManagers(ctx)

	for _, mgr := range managers {
		if err := w.syncRepo(ctx, mgr); err != nil {
			log.Error().Err(err).Str("repo", mgr.RepoURL()).Msg("git-watcher: sync failed")
		}
	}
}

func (w *GitWatcher) currentManagers(ctx context.Context) []*git.Manager {
	mgrs := []*git.Manager{w.managers[w.cfg.DefaultRepoURL]}

	integrations, err := db.AllIntegrations(ctx, w.pool)
	if err != nil {
		log.Error().Err(err).Msg("git-watcher: loading integrations")
		return mgrs
	}

	for _, ig := range integrations {
		if _, ok := w.managers[ig.RepoURL]; ok {
			mgrs = append(mgrs, w.managers[ig.RepoURL])
			continue
		}
		token, err := crypto.DecryptToken(w.cfg.EncryptionKey, ig.TokenEncrypted)
		if err != nil {
			log.Warn().Err(err).Str("repo", ig.RepoURL).Msg("git-watcher: decrypt token failed, skipping")
			continue
		}
		mgr := git.New(git.RepoConfig{
			RepoURL:   ig.RepoURL,
			Branch:    ig.Branch,
			Username:  ig.Provider,
			Token:     token,
			LocalBase: w.cfg.RepoLocalPath,
		})
		w.managers[ig.RepoURL] = mgr
		mgrs = append(mgrs, mgr)
	}
	return mgrs
}

func (w *GitWatcher) syncRepo(ctx context.Context, mgr *git.Manager) error {
	if err := mgr.EnsureCloned(); err != nil {
		return err
	}

	lastSHA, err := db.GetSyncState(ctx, w.pool, mgr.RepoURL(), mgr.Branch())
	if err != nil {
		return err
	}

	commits, err := mgr.CommitsSince(lastSHA)
	if err != nil {
		return err
	}
	if len(commits) == 0 {
		return nil
	}

	log.Info().Str("repo", mgr.RepoURL()).Int("commits", len(commits)).Msg("git-watcher: new commits")

	for _, c := range commits {
		w.processCommit(ctx, mgr, c)
	}

	// Advance sync state to the latest commit.
	newSHA := commits[len(commits)-1].SHA
	return db.SetSyncState(ctx, w.pool, mgr.RepoURL(), mgr.Branch(), newSHA)
}

func (w *GitWatcher) processCommit(ctx context.Context, mgr *git.Manager, c git.Commit) {
	for _, filePath := range c.Files {
		if m := namespacePolicyPathRe.FindStringSubmatch(filePath); m != nil {
			w.syncNamespacePolicyFile(ctx, mgr, filePath, m[1], c)
			continue
		}
		if m := projectPathRe.FindStringSubmatch(filePath); m != nil {
			w.syncProjectFile(ctx, mgr, filePath, m[1], c)
			continue
		}
		if m := appPathRe.FindStringSubmatch(filePath); m != nil {
			w.syncAppFile(ctx, mgr, filePath, m[1], m[2], m[3], c)
			continue
		}
		if m := resourcesValuesPathRe.FindStringSubmatch(filePath); m != nil {
			w.syncResourcesValuesFile(ctx, mgr, filePath, m[1], m[2], c)
			continue
		}
		if m := chartTemplatePathRe.FindStringSubmatch(filePath); m != nil {
			w.syncChartTemplateFile(ctx, mgr, filePath, m[1], m[2], c)
			continue
		}
		if m := valuesPathRe.FindStringSubmatch(filePath); m != nil {
			w.notifyValuesChange(mgr, filePath, m[1], m[2], m[3])
		}
	}
}

// notifyValuesChange reads the current values.yaml and pushes it to any open
// WS editor sessions for that app. Runs best-effort; errors are logged only.
func (w *GitWatcher) notifyValuesChange(mgr *git.Manager, filePath, project, env, app string) {
	if w.notifier == nil {
		return
	}
	content, err := mgr.ReadFile(filePath)
	if err != nil {
		log.Warn().Err(err).Str("path", filePath).Msg("git-watcher: read values for ws notify")
		return
	}
	w.notifier.Notify(project, env, app, "values.yaml", content)
	log.Debug().Str("app", app).Msg("git-watcher: notified ws clients of values change")
}

func (w *GitWatcher) syncProjectFile(ctx context.Context, mgr *git.Manager, filePath, projectSlug string, c git.Commit) {
	content, err := mgr.ReadFileAtCommit(c.SHA, filePath)
	if err != nil {
		log.Warn().Err(err).Str("project", projectSlug).Str("path", filePath).Msg("git-watcher: read project manifest")
		return
	}

	var manifest projectManifest
	if err := yaml.Unmarshal([]byte(content), &manifest); err != nil {
		log.Warn().Err(err).Str("project", projectSlug).Str("path", filePath).Msg("git-watcher: parse project manifest")
		return
	}

	name := manifest.Project
	if name == "" {
		name = projectSlug
	}
	displayName := manifest.DisplayName
	if displayName == "" {
		displayName = name
	}
	ownerType := manifest.OwnerType
	if ownerType == "" {
		ownerType = "team"
	}
	defaultEnvironment := manifest.DefaultEnvironment
	if defaultEnvironment == "" {
		defaultEnvironment = "prod"
	}
	quotas := manifest.Quotas
	if quotas == nil {
		quotas = map[string]any{}
	}

	quotasJSON, _ := json.Marshal(quotas)
	if err := db.UpsertProject(ctx, w.pool, name, displayName, ownerType, defaultEnvironment, quotasJSON); err != nil {
		log.Error().Err(err).Str("project", projectSlug).Str("path", filePath).Msg("git-watcher: upsert project")
		return
	}

	// Upsert environments declared in the manifest.
	envs := manifest.Environments
	if len(envs) == 0 && defaultEnvironment != "" {
		envs = []envManifest{{Name: defaultEnvironment}}
	}
	for _, e := range envs {
		if err := db.UpsertEnvironment(ctx, w.pool, name, e.Name, e.Namespace, e.Type); err != nil {
			log.Error().Err(err).Str("project", name).Str("env", e.Name).Msg("git-watcher: upsert environment")
		}
	}

	if err := db.InsertCommit(ctx, w.pool,
		c.SHA, mgr.RepoURL(), mgr.Branch(), filePath, c.Message,
		c.Author, c.Email, nil, "manual",
	); err != nil {
		log.Warn().Err(err).Str("sha", c.SHA).Msg("git-watcher: record project commit")
	}

	log.Info().Str("project", name).Str("path", filePath).Msg("git-watcher: synced project manifest")
}

// resolveOrCreateProjectEnv returns the project + environment IDs for the given
// slugs, auto-creating both (and granting platform admins) when the git path
// references a project/env not yet known to the DB. Git is the source of truth,
// so a manually-committed manifest can introduce a new project/env.
func (w *GitWatcher) resolveOrCreateProjectEnv(ctx context.Context, projectSlug, envSlug string) (uuid.UUID, uuid.UUID, error) {
	var projectID, environmentID uuid.UUID
	err := w.pool.QueryRow(ctx, `
		SELECT p.id, e.id
		FROM projects p JOIN environments e ON e.project_id = p.id
		WHERE p.name = $1 AND e.name = $2
	`, projectSlug, envSlug).Scan(&projectID, &environmentID)
	if err == nil {
		return projectID, environmentID, nil
	}

	log.Info().Str("project", projectSlug).Str("env", envSlug).Msg("git-watcher: auto-creating project/env from git path")
	if err := db.UpsertProject(ctx, w.pool, projectSlug, projectSlug, "team", envSlug, nil); err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("auto-create project: %w", err)
	}
	if err := db.UpsertEnvironment(ctx, w.pool, projectSlug, envSlug, "", ""); err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("auto-create environment: %w", err)
	}
	if err := db.AddPlatformAdminsToProject(ctx, w.pool, projectSlug); err != nil {
		log.Error().Err(err).Str("project", projectSlug).Msg("git-watcher: add platform admins failed")
	}
	if err := w.pool.QueryRow(ctx, `
		SELECT p.id, e.id
		FROM projects p JOIN environments e ON e.project_id = p.id
		WHERE p.name = $1 AND e.name = $2
	`, projectSlug, envSlug).Scan(&projectID, &environmentID); err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("resolve after auto-create: %w", err)
	}
	return projectID, environmentID, nil
}

// resourceOwnerApps are the synthetic per-project owner apps that carry
// standalone (appRef-less) sibling CRs (databases, buckets, models). They exist
// only so ArgoCD renders those CRs via the shared app-resources chart; they have
// no workload of their own and must never appear in the console's Applications
// list. Mirrors renderer.StandaloneOwnerApp ("<type>-<project>").
func isResourceOwnerApp(appName, projectSlug string) bool {
	for _, t := range []string{"service-databases", "s3-buckets", "models"} {
		if appName == t+"-"+projectSlug {
			return true
		}
	}
	return false
}

func (w *GitWatcher) syncAppFile(ctx context.Context, mgr *git.Manager, filePath, projectSlug, envSlug, appName string, c git.Commit) {
	projectID, environmentID, err := w.resolveOrCreateProjectEnv(ctx, projectSlug, envSlug)
	if err != nil {
		log.Error().Err(err).Str("project", projectSlug).Str("env", envSlug).Msg("git-watcher: resolve project/env")
		return
	}

	// Owner apps are plumbing, not workloads: never surface them as an App, and
	// purge any snapshot a prior indexing pass created.
	if isResourceOwnerApp(appName, projectSlug) {
		if n, derr := db.DeleteSnapshot(ctx, w.pool, projectID, &environmentID, "App", appName); derr != nil {
			log.Error().Err(derr).Str("app", appName).Msg("git-watcher: purge owner-app snapshot")
		} else if n > 0 {
			log.Info().Str("app", appName).Msg("git-watcher: purged synthetic owner-app snapshot")
		}
		return
	}

	summaryJSON, _ := json.Marshal(map[string]any{
		"git_sha":     c.SHA,
		"git_message": c.Message,
		"git_author":  c.Author,
		"app_name":    appName,
		"status":      "Unknown",
		"message":     "Synced from git",
	})

	envUUID := &environmentID
	if err := db.UpsertSnapshot(ctx, w.pool,
		projectID, envUUID,
		"App", appName, "Unknown", summaryJSON, c.When,
	); err != nil {
		log.Error().Err(err).Str("app", appName).Msg("git-watcher: upsert snapshot")
		return
	}

	// Record the commit in git_commits (no operation_id — originated in git).
	if err := db.InsertCommit(ctx, w.pool,
		c.SHA, mgr.RepoURL(), mgr.Branch(), filePath, c.Message,
		c.Author, c.Email, nil, "manual",
	); err != nil {
		log.Warn().Err(err).Str("sha", c.SHA).Msg("git-watcher: record commit")
	}
}

// resourcesValuesManifest models an app's resources.values.yaml: a top-level
// manifests: list, each entry a full CR. Used to reverse-sync every child CR
// into resource_snapshots when the file is committed (ADR 0005).
type resourcesValuesManifest struct {
	Manifests []resourceManifest `yaml:"manifests"`
}

// syncResourcesValuesFile reverse-syncs every CR in an app's resources.values.yaml
// (ServiceDatabase, AIModel, PublicApi, S3Bucket, ...) into resource_snapshots,
// so resources introduced or edited by a manual git commit show up in the
// console. Each entry is upserted by its (kind, name); the upsert is LWW on the
// commit time, so it never clobbers a fresher snapshot already written by the
// API at request time. Supersedes the per-file resources/templates sync.
func (w *GitWatcher) syncResourcesValuesFile(ctx context.Context, mgr *git.Manager, filePath, projectSlug, envSlug string, c git.Commit) {
	content, err := mgr.ReadFileAtCommit(c.SHA, filePath)
	if err != nil {
		log.Warn().Err(err).Str("path", filePath).Msg("git-watcher: read resources.values.yaml")
		return
	}

	var doc resourcesValuesManifest
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		log.Warn().Err(err).Str("path", filePath).Msg("git-watcher: parse resources.values.yaml")
		return
	}

	projectID, environmentID, err := w.resolveOrCreateProjectEnv(ctx, projectSlug, envSlug)
	if err != nil {
		log.Error().Err(err).Str("project", projectSlug).Str("env", envSlug).Msg("git-watcher: resolve project/env")
		return
	}
	envUUID := &environmentID

	synced := 0
	for _, manifest := range doc.Manifests {
		if manifest.Kind == "" || manifest.Metadata.Name == "" {
			log.Warn().Str("path", filePath).Msg("git-watcher: manifest entry missing kind/name, skipping")
			continue
		}
		summaryJSON, _ := json.Marshal(map[string]any{
			"git_sha":     c.SHA,
			"git_message": c.Message,
			"git_author":  c.Author,
			"name":        manifest.Metadata.Name,
			"kind":        manifest.Kind,
			"status":      "Unknown",
			"message":     "Synced from git",
			"spec":        manifest.Spec,
		})
		if err := db.UpsertSnapshot(ctx, w.pool,
			projectID, envUUID,
			manifest.Kind, manifest.Metadata.Name, "Unknown", summaryJSON, c.When,
		); err != nil {
			log.Error().Err(err).Str("kind", manifest.Kind).Str("name", manifest.Metadata.Name).Msg("git-watcher: upsert resource snapshot")
			continue
		}
		synced++
	}

	if err := db.InsertCommit(ctx, w.pool,
		c.SHA, mgr.RepoURL(), mgr.Branch(), filePath, c.Message,
		c.Author, c.Email, nil, "manual",
	); err != nil {
		log.Warn().Err(err).Str("sha", c.SHA).Msg("git-watcher: record resource commit")
	}

	log.Info().Int("count", synced).Str("path", filePath).Msg("git-watcher: synced resources from git")
}

// chartCRKinds are the platform CR kinds the console indexes from a helm chart's
// templates. Other rendered objects (Deployments, ConfigMaps, StatefulSets, ...)
// are ignored so resource_snapshots stays a view of platform resources rather
// than raw workloads.
var chartCRKinds = map[string]bool{
	"S3Bucket":          true,
	"ServiceDatabaseV2": true,
	"ServiceDatabase":   true,
	"PublicApi":         true,
	"AIModel":           true,
}

// helmActionRe matches an inline helm/sprig action so a chart template can be
// parsed as YAML for indexing.
var helmActionRe = regexp.MustCompile(`\{\{-?.*?-?\}\}`)

const helmActionPlaceholder = "__templated__"

// sanitizeHelmTemplate turns a helm chart template into parseable YAML for
// indexing without rendering it: whole-line control-flow actions ({{- if }},
// {{ range }}, {{ end }}) are dropped, and inline value actions become a
// placeholder scalar. Only literal fields (kind, a literal metadata.name)
// survive intact — which is all the resource index needs; a CR whose name is
// itself templated is skipped downstream.
func sanitizeHelmTemplate(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "{{") {
			continue
		}
		out = append(out, helmActionRe.ReplaceAllString(line, `"`+helmActionPlaceholder+`"`))
	}
	return strings.Join(out, "\n")
}

// syncChartTemplateFile indexes the platform CRs declared in an app's helm chart
// template into resource_snapshots, so buckets/databases delivered by a chart
// (mimir, opensearch, ...) appear in the console alongside those declared via the
// resources.values.yaml passthrough. Helm values are not rendered; only literal
// CR identity (kind + literal metadata.name) is indexed.
func (w *GitWatcher) syncChartTemplateFile(ctx context.Context, mgr *git.Manager, filePath, projectSlug, envSlug string, c git.Commit) {
	content, err := mgr.ReadFileAtCommit(c.SHA, filePath)
	if err != nil {
		log.Warn().Err(err).Str("path", filePath).Msg("git-watcher: read chart template")
		return
	}

	projectID, environmentID, err := w.resolveOrCreateProjectEnv(ctx, projectSlug, envSlug)
	if err != nil {
		log.Error().Err(err).Str("project", projectSlug).Str("env", envSlug).Msg("git-watcher: resolve project/env")
		return
	}
	envUUID := &environmentID

	dec := yaml.NewDecoder(strings.NewReader(sanitizeHelmTemplate(content)))
	synced := 0
	for {
		var m resourceManifest
		if err := dec.Decode(&m); err != nil {
			break
		}
		if !chartCRKinds[m.Kind] {
			continue
		}
		if m.Metadata.Name == "" || strings.Contains(m.Metadata.Name, helmActionPlaceholder) {
			continue
		}
		summaryJSON, _ := json.Marshal(map[string]any{
			"git_sha":     c.SHA,
			"git_message": c.Message,
			"git_author":  c.Author,
			"name":        m.Metadata.Name,
			"kind":        m.Kind,
			"status":      "Unknown",
			"message":     "Synced from git (helm chart template)",
			"spec":        m.Spec,
		})
		if err := db.UpsertSnapshot(ctx, w.pool,
			projectID, envUUID,
			m.Kind, m.Metadata.Name, "Unknown", summaryJSON, c.When,
		); err != nil {
			log.Error().Err(err).Str("kind", m.Kind).Str("name", m.Metadata.Name).Msg("git-watcher: upsert chart-template snapshot")
			continue
		}
		synced++
	}
	if synced > 0 {
		log.Info().Int("count", synced).Str("path", filePath).Msg("git-watcher: synced chart-template resources")
	}
}

func (w *GitWatcher) syncNamespacePolicyFile(ctx context.Context, mgr *git.Manager, filePath, namespace string, c git.Commit) {
	content, err := mgr.ReadFileAtCommit(c.SHA, filePath)
	if err != nil {
		log.Warn().Err(err).Str("namespace", namespace).Str("path", filePath).Msg("git-watcher: read namespace-policy manifest")
		return
	}

	var manifest namespacePolicyManifest
	if err := yaml.Unmarshal([]byte(content), &manifest); err != nil {
		log.Warn().Err(err).Str("namespace", namespace).Str("path", filePath).Msg("git-watcher: parse namespace-policy manifest")
		return
	}

	ns := manifest.Namespace
	if ns == "" {
		ns = namespace
	}

	limitRangeJSON, _ := json.Marshal(manifest.LimitRange)
	resourceQuotaJSON, _ := json.Marshal(manifest.ResourceQuota)

	if err := db.UpsertEnvironmentPolicy(ctx, w.pool, ns, limitRangeJSON, resourceQuotaJSON); err != nil {
		log.Error().Err(err).Str("namespace", ns).Msg("git-watcher: upsert environment policy")
		return
	}

	log.Info().Str("namespace", ns).Str("path", filePath).Msg("git-watcher: synced namespace-policy")
}
