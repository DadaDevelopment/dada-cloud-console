package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repo is the join of git_repos + its environment/project, carrying everything
// the runner needs to drive a build: clone target, provider creds source, build
// configuration, and the project slug used for Harbor + image URIs.
type Repo struct {
	ID                uuid.UUID
	ProjectID         uuid.UUID
	ProjectSlug       string // projects.name — Harbor project slug
	EnvironmentID     uuid.UUID
	Namespace         string // env k8s namespace (deploy target)
	AppName           string
	Provider          string // github | gitlab
	RepoFullName      string // org/repo
	CloneURL          string
	TokenEncrypted    []byte // GitLab only; nil for GitHub App
	WebhookSecret     string
	ProductionBranch  string
	RootDir           string
	FrameworkOverride string
	AutoDeploy        bool

	// Intended app spec, applied when the FIRST successful build creates the app
	// (CreateApp). Ignored once the app exists (deploys then use DeployImageVersion).
	Port     int
	Replicas int
	Profile  string

	// GitHub App installation (numeric id from git_app_installations).
	InstallationID int64
}

const repoSelect = `
	SELECT r.id, r.project_id, p.name, r.environment_id, e.namespace, r.app_name,
	       r.provider, r.repo_full_name, r.clone_url, r.token_encrypted,
	       COALESCE(r.webhook_secret, ''), r.production_branch, r.root_dir,
	       COALESCE(r.framework_override, ''), r.auto_deploy,
	       r.port, r.replicas, r.profile,
	       COALESCE(i.installation_id, 0)
	FROM   git_repos r
	JOIN   projects p     ON p.id = r.project_id
	JOIN   environments e ON e.id = r.environment_id
	LEFT   JOIN git_app_installations i ON i.id = r.installation_id
`

func scanRepo(row pgx.Row) (*Repo, error) {
	var rp Repo
	if err := row.Scan(
		&rp.ID, &rp.ProjectID, &rp.ProjectSlug, &rp.EnvironmentID, &rp.Namespace, &rp.AppName,
		&rp.Provider, &rp.RepoFullName, &rp.CloneURL, &rp.TokenEncrypted,
		&rp.WebhookSecret, &rp.ProductionBranch, &rp.RootDir,
		&rp.FrameworkOverride, &rp.AutoDeploy,
		&rp.Port, &rp.Replicas, &rp.Profile, &rp.InstallationID,
	); err != nil {
		return nil, err
	}
	return &rp, nil
}

// LoadRepo loads a git_repos row (with project/env context) by id.
func LoadRepo(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (*Repo, error) {
	rp, err := scanRepo(pool.QueryRow(ctx, repoSelect+` WHERE r.id = $1`, id))
	if err != nil {
		return nil, fmt.Errorf("load repo %s: %w", id, err)
	}
	return rp, nil
}

// ResolveReposByFullName returns all git_repos linked to a repo_full_name. A
// single GitHub repo may back several apps/environments, so this is a slice.
// Webhook dispatch fans a push out to every matching repo on the pushed branch.
func ResolveReposByFullName(ctx context.Context, pool *pgxpool.Pool, fullName string) ([]*Repo, error) {
	rows, err := pool.Query(ctx, repoSelect+` WHERE r.repo_full_name = $1`, fullName)
	if err != nil {
		return nil, fmt.Errorf("resolve repos %q: %w", fullName, err)
	}
	defer rows.Close()
	var out []*Repo
	for rows.Next() {
		rp, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rp)
	}
	return out, rows.Err()
}
