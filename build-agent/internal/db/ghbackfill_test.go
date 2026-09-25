package db

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedAppRepo(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName string, installationID int64) uuid.UUID {
	t.Helper()
	instRow := uuid.New()
	exec(t, pool,
		`INSERT INTO git_app_installations (id, project_id, provider, installation_id, account_login, account_type, org_id)
		 VALUES ($1, $2, 'github', $3, 'org', 'Organization', $4)`,
		instRow, projectID, installationID, "test-org-"+projectID.String())
	repoID := uuid.New()
	exec(t, pool,
		`INSERT INTO git_repos (id, project_id, environment_id, app_name, provider, repo_full_name, clone_url, profile, installation_id)
		 VALUES ($1, $2, $3, $4, 'github', $5, 'https://example.com/x.git', 'small', $6)`,
		repoID, projectID, envID, appName, "org/"+appName+"-"+uuid.NewString()[:6], instRow)
	return repoID
}

func seedLiveSnapshot(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName, image, status string) {
	t.Helper()
	summary, _ := json.Marshal(map[string]any{"image": image, "status": status, "url": "https://" + appName + ".example.com"})
	exec(t, pool,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, 'App', $3, $4, $5, NOW())`,
		projectID, envID, appName, status, summary)
}

func backfillCandidateFor(t *testing.T, pool *pgxpool.Pool, buildID uuid.UUID) *GitHubBackfillCandidate {
	t.Helper()
	all, err := ListGitHubBackfillCandidates(context.Background(), pool)
	if err != nil {
		t.Fatalf("ListGitHubBackfillCandidates: %v", err)
	}
	for i := range all {
		if all[i].BuildID == buildID {
			return &all[i]
		}
	}
	return nil
}

// TestGitHubBackfillPicksTheRunningBuildOfReposWithNoHistory pins what the
// one-off backfill names: the build whose image the app is running, not the
// newest build, and never a repo that already has a GitHub Deployment.
func TestGitHubBackfillPicksTheRunningBuildOfReposWithNoHistory(t *testing.T) {
	pool := testPool(t)
	projectID, envID := seedProjectEnv(t, pool, "small")

	app := "bf-" + uuid.NewString()[:6]
	repo := seedAppRepo(t, pool, projectID, envID, app, 4242)
	running := seedBuild(t, pool, repo, envID, app, "aaa111")
	newer := seedBuild(t, pool, repo, envID, app, "bbb222")
	exec(t, pool, `UPDATE builds SET image_uri = 'img@sha256:running', created_at = NOW() - interval '1 hour' WHERE id = $1`, running.ID)
	exec(t, pool, `UPDATE builds SET image_uri = 'img@sha256:newer-not-live' WHERE id = $1`, newer.ID)
	seedLiveSnapshot(t, pool, projectID, envID, app, "img@sha256:running", "Ready")

	c := backfillCandidateFor(t, pool, running.ID)
	if c == nil {
		t.Fatal("the running build of a repo with no deployment history was not offered")
	}
	if c.Ref != "aaa111" || c.InstallationID != 4242 || c.LiveStatus != "Ready" || c.LiveURL == "" {
		t.Fatalf("candidate = %+v", *c)
	}
	if backfillCandidateFor(t, pool, newer.ID) != nil {
		t.Fatal("a build the app is not running was offered")
	}

	exec(t, pool, `UPDATE builds SET gh_deployment_state = 'failure' WHERE id = $1`, newer.ID)
	if backfillCandidateFor(t, pool, running.ID) != nil {
		t.Fatal("a repo that already had a GitHub Deployment was backfilled again")
	}
}
