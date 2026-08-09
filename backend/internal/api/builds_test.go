package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func newTriggerBuildCtx(projectID, envID uuid.UUID, appName string, claims *auth.Claims) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	path := "/api/v1/projects/" + projectID.String() + "/environments/" + envID.String() + "/apps/" + appName + "/builds"
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "envId", Value: envID.String()},
		{Key: "appName", Value: appName},
	}
	auth.SetClaims(c, claims)
	return c, rec
}

// TestTriggerBuild_ArchiveRepo_SetsHeadSHAAndSource covers the rebuild path
// for an app deployed from an uploaded archive: build-agent's
// shouldResolveHeadCommit only resolves HEAD for provider=="github", so
// without this, a rebuilt archive build's head_sha stays NULL forever and
// the console has nothing distinguishing one rebuild from the next. The
// handler must derive head_sha from the git_repos.clone_url itself and tag
// the response with source="archive".
func TestTriggerBuild_ArchiveRepo_SetsHeadSHAAndSource(t *testing.T) {
	pool := testSourceArchivePool(t)
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "acme")
	cloneURL := "s3://test-bucket/source-uploads/" + projectID.String() + "/" + appName + "/abcdef01-2222-uuid.tar.gz"
	seedArchiveGitRepo(t, pool, projectID, envID, appName, cloneURL)

	h := &Handler{pool: pool}
	claims := godClaims(seedUser(t, pool))
	c, rec := newTriggerBuildCtx(projectID, envID, appName, claims)
	h.TriggerBuild(c)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s want 202", rec.Code, rec.Body.String())
	}

	var gitRepoID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM git_repos WHERE project_id=$1 AND environment_id=$2 AND app_name=$3`,
		projectID, envID, appName,
	).Scan(&gitRepoID); err != nil {
		t.Fatalf("lookup git_repos row: %v", err)
	}

	var headSHA *string
	if err := pool.QueryRow(context.Background(),
		`SELECT head_sha FROM builds WHERE git_repo_id = $1`, gitRepoID,
	).Scan(&headSHA); err != nil {
		t.Fatalf("read queued build: %v", err)
	}
	if headSHA == nil || *headSHA != "abcdef01" {
		t.Fatalf("head_sha = %v, want \"abcdef01\"", headSHA)
	}
}

// TestTriggerBuild_GitRepo_LeavesHeadSHANil covers the non-archive rebuild
// path: a git-linked repo has no clone_url shaped like an upload key, so
// TriggerBuild must not invent a head_sha for it — build-agent resolves the
// real one via the GitHub API afterwards.
func TestTriggerBuild_GitRepo_LeavesHeadSHANil(t *testing.T) {
	pool := testSourceArchivePool(t)
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "acme")
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO git_repos (project_id, environment_id, app_name, provider, repo_full_name, clone_url, production_branch)
		 VALUES ($1, $2, $3, 'github', $4, $5, 'main')`,
		projectID, envID, appName, "acme/"+appName, "https://github.com/acme/"+appName+".git",
	); err != nil {
		t.Fatalf("seed git_repos: %v", err)
	}

	h := &Handler{pool: pool}
	claims := godClaims(seedUser(t, pool))
	c, rec := newTriggerBuildCtx(projectID, envID, appName, claims)
	h.TriggerBuild(c)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s want 202", rec.Code, rec.Body.String())
	}

	var gitRepoID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM git_repos WHERE project_id=$1 AND environment_id=$2 AND app_name=$3`,
		projectID, envID, appName,
	).Scan(&gitRepoID); err != nil {
		t.Fatalf("lookup git_repos row: %v", err)
	}

	var headSHA *string
	if err := pool.QueryRow(context.Background(),
		`SELECT head_sha FROM builds WHERE git_repo_id = $1`, gitRepoID,
	).Scan(&headSHA); err != nil {
		t.Fatalf("read queued build: %v", err)
	}
	if headSHA != nil {
		t.Fatalf("head_sha = %q, want nil for a github-provider repo", *headSHA)
	}
}
