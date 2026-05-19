package worker

import (
	"context"
	"encoding/json"
	"regexp"
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

// projectPathRe matches clusters/<cluster>/projects/<project>/project.yaml.
// Capture group 1 is the project slug.
var projectPathRe = regexp.MustCompile(`^clusters/[^/]+/projects/([^/]+)/project\.yaml$`)

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
		if m := projectPathRe.FindStringSubmatch(filePath); m != nil {
			w.syncProjectFile(ctx, mgr, filePath, m[1], c)
			continue
		}
		m := appPathRe.FindStringSubmatch(filePath)
		if m == nil {
			continue
		}
		projectSlug, envSlug, appName := m[1], m[2], m[3]
		w.syncAppFile(ctx, mgr, filePath, projectSlug, envSlug, appName, c)
	}
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

func (w *GitWatcher) syncAppFile(ctx context.Context, mgr *git.Manager, filePath, projectSlug, envSlug, appName string, c git.Commit) {
	// Resolve project + environment IDs, auto-creating them if absent.
	var projectID uuid.UUID
	var environmentID uuid.UUID
	err := w.pool.QueryRow(ctx, `
		SELECT p.id, e.id
		FROM projects p JOIN environments e ON e.project_id = p.id
		WHERE p.name = $1 AND e.name = $2
	`, projectSlug, envSlug).Scan(&projectID, &environmentID)
	if err != nil {
		log.Info().Str("project", projectSlug).Str("env", envSlug).Msg("git-watcher: auto-creating project/env from git path")
		if err := db.UpsertProject(ctx, w.pool, projectSlug, projectSlug, "team", envSlug, nil); err != nil {
			log.Error().Err(err).Str("project", projectSlug).Msg("git-watcher: auto-create project failed")
			return
		}
		if err := db.UpsertEnvironment(ctx, w.pool, projectSlug, envSlug, "", ""); err != nil {
			log.Error().Err(err).Str("project", projectSlug).Str("env", envSlug).Msg("git-watcher: auto-create environment failed")
			return
		}
		if err := db.AddPlatformAdminsToProject(ctx, w.pool, projectSlug); err != nil {
			log.Error().Err(err).Str("project", projectSlug).Msg("git-watcher: add platform admins failed")
		}
		if err := w.pool.QueryRow(ctx, `
			SELECT p.id, e.id
			FROM projects p JOIN environments e ON e.project_id = p.id
			WHERE p.name = $1 AND e.name = $2
		`, projectSlug, envSlug).Scan(&projectID, &environmentID); err != nil {
			log.Error().Err(err).Str("project", projectSlug).Str("env", envSlug).Msg("git-watcher: resolve after auto-create failed")
			return
		}
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
