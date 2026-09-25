package db

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GitHub Deployment lifecycle as stored on builds.gh_deployment_state. Opening
// is the claim taken before the API call, so a build re-entering execute after
// an agent restart never opens a second GitHub Deployment for the same commit.
// Unavailable records that GitHub refused the deployment (most often the App
// installation has not accepted the Deployments permission yet) and ends the
// attempt for that build.
const (
	GHDeployOpening     = "opening"
	GHDeployInProgress  = "in_progress"
	GHDeployUnavailable = "unavailable"
)

// GitHubEnvironment is what a build's GitHub Deployment is filed under: the
// environment's name and type, and how many apps share the build's repo, since
// several apps deploying one repo into one GitHub environment would keep
// marking each other inactive.
type GitHubEnvironment struct {
	Name     string
	Type     string
	RepoApps int
}

// LoadGitHubEnvironment reads the environment and repo fan-out for a build.
func LoadGitHubEnvironment(ctx context.Context, pool *pgxpool.Pool, envID uuid.UUID, repoFullName string) (GitHubEnvironment, error) {
	var g GitHubEnvironment
	err := pool.QueryRow(ctx, `
		SELECT e.name, COALESCE(e.type, ''),
		       (SELECT count(*) FROM git_repos WHERE repo_full_name = $2)
		FROM   environments e
		WHERE  e.id = $1
	`, envID, repoFullName).Scan(&g.Name, &g.Type, &g.RepoApps)
	if err != nil {
		return g, fmt.Errorf("load github environment %s: %w", envID, err)
	}
	return g, nil
}

// ClaimGitHubDeployment takes the right to open a GitHub Deployment for a build.
// It returns false while one is open or was refused. A build whose deployment
// already settled as failure or error may claim again: the platform-failure
// retry runs the same build row a second time, and that attempt deserves its
// own deployment rather than inheriting the red one.
func ClaimGitHubDeployment(ctx context.Context, pool *pgxpool.Pool, buildID uuid.UUID) (bool, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE builds
		SET    gh_deployment_state = $2
		WHERE  id = $1
		  AND  (gh_deployment_state IS NULL OR gh_deployment_state IN ('failure', 'error'))
	`, buildID, GHDeployOpening)
	if err != nil {
		return false, fmt.Errorf("claim github deployment %s: %w", buildID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// SetGitHubDeployment records the opened GitHub Deployment, or with id 0 and
// state unavailable, that GitHub refused it.
func SetGitHubDeployment(ctx context.Context, pool *pgxpool.Pool, buildID uuid.UUID, deploymentID, installationID int64, state string) error {
	var id, inst any
	if deploymentID != 0 {
		id, inst = deploymentID, installationID
	}
	_, err := pool.Exec(ctx, `
		UPDATE builds
		SET    gh_deployment_id = $2, gh_installation_id = $3, gh_deployment_state = $4
		WHERE  id = $1
	`, buildID, id, inst, state)
	if err != nil {
		return fmt.Errorf("set github deployment %s: %w", buildID, err)
	}
	return nil
}

// MoveGitHubDeployment is a compare-and-set on gh_deployment_state, so only one
// build-agent replica posts a given transition to GitHub.
func MoveGitHubDeployment(ctx context.Context, pool *pgxpool.Pool, buildID uuid.UUID, from, to string) (bool, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE builds SET gh_deployment_state = $3
		WHERE  id = $1 AND gh_deployment_state = $2
	`, buildID, from, to)
	if err != nil {
		return false, fmt.Errorf("move github deployment %s: %w", buildID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// OpenGitHubDeployment is a build whose GitHub Deployment still reads in
// progress, joined with everything that decides its outcome: the build's own
// verdict, the deploy it handed off, that deploy's operation, and the image the
// app is running right now (resource_snapshots, fed by the k8s status
// reconciler and by the VM stack sync alike).
type OpenGitHubDeployment struct {
	BuildID        uuid.UUID
	RepoFullName   string
	ProjectSlug    string
	AppName        string
	InstallationID int64
	DeploymentID   int64
	BuildStatus    string
	BuildImage     string
	BuildUpdatedAt time.Time
	HasDeploy      bool
	DeployImage    string
	OpStatus       string
	OpError        string
	LiveImage      string
	LiveStatus     string
	LiveURL        string
	Superseded     bool
}

// ListOpenGitHubDeployments returns up to limit builds whose GitHub Deployment is
// still in progress, oldest first.
func ListOpenGitHubDeployments(ctx context.Context, pool *pgxpool.Pool, limit int) ([]OpenGitHubDeployment, error) {
	rows, err := pool.Query(ctx, `
		SELECT b.id, COALESCE(gr.repo_full_name, ''), COALESCE(p.name, ''), b.app_name,
		       b.gh_installation_id, b.gh_deployment_id,
		       b.status, COALESCE(b.image_uri, ''), b.updated_at,
		       d.id IS NOT NULL, COALESCE(d.image_uri, ''),
		       COALESCE(o.status, ''), COALESCE(o.error_message, ''),
		       COALESCE(rs.summary_json->>'image', ''),
		       COALESCE(rs.summary_json->>'status', ''),
		       COALESCE(rs.summary_json->>'url', ''),
		       d.id IS NOT NULL AND EXISTS (
		           SELECT 1 FROM deployments d2
		           WHERE  d2.environment_id = b.environment_id
		             AND  d2.app_name = b.app_name
		             AND  d2.created_at > d.created_at
		       )
		FROM   builds b
		LEFT JOIN git_repos gr ON gr.id = b.git_repo_id
		LEFT JOIN environments e ON e.id = b.environment_id
		LEFT JOIN projects p ON p.id = e.project_id
		LEFT JOIN LATERAL (
		       SELECT id, image_uri, operation_id, created_at
		       FROM   deployments
		       WHERE  build_id = b.id
		       ORDER  BY created_at DESC
		       LIMIT  1
		) d ON true
		LEFT JOIN operations o ON o.id = d.operation_id
		LEFT JOIN resource_snapshots rs
		       ON rs.environment_id = b.environment_id AND rs.kind = 'App' AND rs.name = b.app_name
		WHERE  b.gh_deployment_state = $1
		  AND  b.gh_deployment_id IS NOT NULL
		  AND  b.gh_installation_id IS NOT NULL
		ORDER  BY b.created_at
		LIMIT  $2
	`, GHDeployInProgress, limit)
	if err != nil {
		return nil, fmt.Errorf("list open github deployments: %w", err)
	}
	defer rows.Close()
	var out []OpenGitHubDeployment
	for rows.Next() {
		var o OpenGitHubDeployment
		if err := rows.Scan(&o.BuildID, &o.RepoFullName, &o.ProjectSlug, &o.AppName, &o.InstallationID, &o.DeploymentID,
			&o.BuildStatus, &o.BuildImage, &o.BuildUpdatedAt,
			&o.HasDeploy, &o.DeployImage, &o.OpStatus, &o.OpError,
			&o.LiveImage, &o.LiveStatus, &o.LiveURL, &o.Superseded); err != nil {
			return nil, fmt.Errorf("scan open github deployment: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// GitHubBackfillCandidate is the build a GitHub App repo is running right now,
// for a repo that has never had a GitHub Deployment: the newest successful
// build whose image the app's live snapshot reports as running.
type GitHubBackfillCandidate struct {
	BuildID        uuid.UUID
	EnvironmentID  uuid.UUID
	RepoFullName   string
	InstallationID int64
	ProjectSlug    string
	AppName        string
	Ref            string
	LiveStatus     string
	LiveURL        string
}

// ListGitHubBackfillCandidates returns, per GitHub App repo that no build has
// ever opened a GitHub Deployment for, the build its app is running now.
// Repos whose running image matches no build of theirs are left out: there is
// no commit to name.
func ListGitHubBackfillCandidates(ctx context.Context, pool *pgxpool.Pool) ([]GitHubBackfillCandidate, error) {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT ON (gr.id)
		       b.id, gr.environment_id, gr.repo_full_name, i.installation_id, p.name, gr.app_name,
		       COALESCE(NULLIF(b.head_sha, ''), b.commit_sha),
		       COALESCE(rs.summary_json->>'status', ''), COALESCE(rs.summary_json->>'url', '')
		FROM   git_repos gr
		JOIN   git_app_installations i ON i.id = gr.installation_id
		JOIN   projects p ON p.id = gr.project_id
		JOIN   resource_snapshots rs
		       ON rs.environment_id = gr.environment_id AND rs.kind = 'App' AND rs.name = gr.app_name
		JOIN   builds b
		       ON b.git_repo_id = gr.id AND b.status = 'success' AND b.image_uri = rs.summary_json->>'image'
		WHERE  gr.provider = 'github'
		  AND  i.installation_id <> 0
		  AND  b.gh_deployment_state IS NULL
		  AND  NOT EXISTS (
		           SELECT 1 FROM builds x
		           WHERE  x.git_repo_id = gr.id AND x.gh_deployment_state IS NOT NULL
		       )
		ORDER  BY gr.id, b.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list github backfill candidates: %w", err)
	}
	defer rows.Close()
	var out []GitHubBackfillCandidate
	for rows.Next() {
		var c GitHubBackfillCandidate
		if err := rows.Scan(&c.BuildID, &c.EnvironmentID, &c.RepoFullName, &c.InstallationID,
			&c.ProjectSlug, &c.AppName, &c.Ref, &c.LiveStatus, &c.LiveURL); err != nil {
			return nil, fmt.Errorf("scan github backfill candidate: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
