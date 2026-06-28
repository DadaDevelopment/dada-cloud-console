package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GitIntegration holds the per-project git configuration from git_integrations.
type GitIntegration struct {
	ID             uuid.UUID
	ProjectID      uuid.UUID
	Provider       string
	RepoURL        string
	Branch         string
	TokenEncrypted []byte
	WebhookSecret  *string
	UsePR          bool
}

// GetIntegration returns the git integration for a project, or nil if none exists.
func GetIntegration(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) (*GitIntegration, error) {
	row := pool.QueryRow(ctx, `
		SELECT id, project_id, provider, repo_url, branch, token_encrypted, webhook_secret, use_pr
		FROM   git_integrations
		WHERE  project_id = $1
	`, projectID)

	var g GitIntegration
	err := row.Scan(
		&g.ID, &g.ProjectID, &g.Provider, &g.RepoURL, &g.Branch,
		&g.TokenEncrypted, &g.WebhookSecret, &g.UsePR,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get git_integration: %w", err)
	}
	return &g, nil
}

// AllIntegrations returns all git_integrations rows (used by the Git Watcher to
// know which repos to poll beyond the default).
func AllIntegrations(ctx context.Context, pool *pgxpool.Pool) ([]GitIntegration, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, project_id, provider, repo_url, branch, token_encrypted, webhook_secret, use_pr
		FROM   git_integrations
	`)
	if err != nil {
		return nil, fmt.Errorf("query git_integrations: %w", err)
	}
	defer rows.Close()

	var result []GitIntegration
	for rows.Next() {
		var g GitIntegration
		if err := rows.Scan(
			&g.ID, &g.ProjectID, &g.Provider, &g.RepoURL, &g.Branch,
			&g.TokenEncrypted, &g.WebhookSecret, &g.UsePR,
		); err != nil {
			return nil, fmt.Errorf("scanning git_integration: %w", err)
		}
		result = append(result, g)
	}
	return result, rows.Err()
}

// GetOrSetSyncState returns the last known SHA for a repo+branch, then updates it.
// Returns the previous SHA (empty string if first time).
func GetSyncState(ctx context.Context, pool *pgxpool.Pool, repoURL, branch string) (string, error) {
	var sha string
	err := pool.QueryRow(ctx, `
		SELECT last_sha FROM git_sync_state WHERE repo_url = $1 AND branch = $2
	`, repoURL, branch).Scan(&sha)
	if err != nil {
		return "", nil // no row yet → first poll
	}
	return sha, nil
}

// SetSyncState upserts the last known SHA for a repo+branch.
func SetSyncState(ctx context.Context, pool *pgxpool.Pool, repoURL, branch, sha string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO git_sync_state (repo_url, branch, last_sha)
		VALUES ($1, $2, $3)
		ON CONFLICT (repo_url, branch) DO UPDATE
		SET last_sha = EXCLUDED.last_sha, updated_at = NOW()
	`, repoURL, branch, sha)
	return err
}
