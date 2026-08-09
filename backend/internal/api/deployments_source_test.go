package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedGitRepo inserts a git_repos row so a build can reference it, cleaning up
// via the project cascade seedOptimisticFixture already registers.
func seedGitRepo(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName, provider string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO git_repos (project_id, environment_id, app_name, provider, repo_full_name, clone_url)
		 VALUES ($1, $2, $3, $4, 'octocat/hello', 'https://example.invalid/octocat/hello.git')
		 RETURNING id`,
		projectID, envID, appName, provider,
	).Scan(&id); err != nil {
		t.Fatalf("seed git repo: %v", err)
	}
	return id
}

// seedBuild inserts a builds row and returns its id.
func seedBuild(t *testing.T, pool *pgxpool.Pool, gitRepoID, envID uuid.UUID, appName, commitSHA, commitMessage, branch string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO builds (git_repo_id, environment_id, app_name, commit_sha, commit_message, branch, status)
		 VALUES ($1, $2, $3, $4, $5, $6, 'success')
		 RETURNING id`,
		gitRepoID, envID, appName, commitSHA, commitMessage, branch,
	).Scan(&id); err != nil {
		t.Fatalf("seed build: %v", err)
	}
	return id
}

// seedDeployment inserts a deployments row, optionally linked to a build.
func seedDeployment(t *testing.T, pool *pgxpool.Pool, envID uuid.UUID, appName string, buildID *uuid.UUID, imageURI string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO deployments (environment_id, app_name, build_id, image_uri, trigger, is_current)
		 VALUES ($1, $2, $3, $4, 'push', true)
		 RETURNING id`,
		envID, appName, buildID, imageURI,
	).Scan(&id); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	return id
}

// TestListDeployments_ReturnsCommitProvenance is the regression gate for the
// console lying about deployment source: the deployments endpoint used to
// select only id/environment_id/app_name/build_id/operation_id/image_uri/
// trigger/is_current/created_at, so the frontend's resolveCommit() always
// fell back to kind:"none" and every row rendered as an "uploaded archive" --
// including plain git deploys. The endpoint must now join through
// builds -> git_repos and return commit_sha/commit_message/branch/source, and
// a deployment with no build_id at all must carry no source rather than a
// fabricated one.
func TestListDeployments_ReturnsCommitProvenance(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)

	gitApp := "git-app-" + uuid.NewString()[:8]
	repoID := seedGitRepo(t, pool, projectID, envID, gitApp, "github")
	buildID := seedBuild(t, pool, repoID, envID, gitApp, "abc123abc123abc123abc123abc123abc123ab", "fix the thing", "main")
	gitDeployID := seedDeployment(t, pool, envID, gitApp, &buildID, "harbor.example/proj/"+gitApp+"@sha256:aaa")

	noBuildApp := "manual-app-" + uuid.NewString()[:8]
	noBuildDeployID := seedDeployment(t, pool, envID, noBuildApp, nil, "harbor.example/proj/"+noBuildApp+"@sha256:bbb")

	c, rec := newGetCtx(t, gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "envId", Value: envID.String()},
		{Key: "appName", Value: gitApp},
	}, godClaims(seedUser(t, pool)))
	h.ListDeployments(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("git app: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Deployments []deployment `json:"deployments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, rec.Body.String())
	}
	if len(body.Deployments) != 1 {
		t.Fatalf("git app: got %d deployments, want 1", len(body.Deployments))
	}
	d := body.Deployments[0]
	if d.ID != gitDeployID {
		t.Fatalf("git app: unexpected deployment id %s", d.ID)
	}
	if d.CommitSHA == nil || *d.CommitSHA != "abc123abc123abc123abc123abc123abc123ab" {
		t.Fatalf("git app: commit_sha = %v, want the seeded sha", d.CommitSHA)
	}
	if d.Branch == nil || *d.Branch != "main" {
		t.Fatalf("git app: branch = %v, want main", d.Branch)
	}
	if d.Source != "git" {
		t.Fatalf("git app: source = %q, want git", d.Source)
	}

	c2, rec2 := newGetCtx(t, gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "envId", Value: envID.String()},
		{Key: "appName", Value: noBuildApp},
	}, godClaims(seedUser(t, pool)))
	h.ListDeployments(c2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("no-build app: status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}
	var body2 struct {
		Deployments []deployment `json:"deployments"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &body2); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, rec2.Body.String())
	}
	if len(body2.Deployments) != 1 {
		t.Fatalf("no-build app: got %d deployments, want 1", len(body2.Deployments))
	}
	d2 := body2.Deployments[0]
	if d2.ID != noBuildDeployID {
		t.Fatalf("no-build app: unexpected deployment id %s", d2.ID)
	}
	if d2.CommitSHA != nil {
		t.Fatalf("no-build app: commit_sha = %v, want nil (no build_id, no fabricated commit)", d2.CommitSHA)
	}
	if d2.Source != "" {
		t.Fatalf("no-build app: source = %q, want empty (no build_id means no known provenance -- must not claim archive or git)", d2.Source)
	}
}
