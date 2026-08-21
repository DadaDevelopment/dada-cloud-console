package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newGetBuildCtx(projectID, buildID uuid.UUID, claims *auth.Claims) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	path := "/api/v1/projects/" + projectID.String() + "/builds/" + buildID.String()
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "buildId", Value: buildID.String()},
	}
	auth.SetClaims(c, claims)
	return c, rec
}

// seedFailedBuildAt inserts one failed build for the given repo at an explicit
// created_at, so a test can control ordering precisely instead of racing
// database clock resolution between inserts.
func seedFailedBuildAt(t *testing.T, pool *pgxpool.Pool, gitRepoID, envID uuid.UUID, appName, commitSHA string, createdAt time.Time, failReason, errorMessage string) uuid.UUID {
	t.Helper()
	var buildID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO builds (git_repo_id, environment_id, app_name, commit_sha, branch, trigger, status, fail_reason, error_message, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, 'main', 'push', 'failed', $5, $6, $7, $7)
		 RETURNING id`,
		gitRepoID, envID, appName, commitSHA, failReason, errorMessage, createdAt,
	).Scan(&buildID); err != nil {
		t.Fatalf("seed failed build %s: %v", commitSHA, err)
	}
	return buildID
}

// TestBuildRepeatCount_ThirdIdenticalFailureReportsThree pins the live defect
// from 2026-08-21: tarotreaderhimu@gmail.com got the same
// dockerfile_build_failed / npm install failure three times in ten minutes
// and, unable to tell the third attempt from the first, created a database
// instead of fixing the repo. GetBuild on the third build in an identical
// streak must report repeat_count=3 so the frontend can say "this is not a
// new problem" instead of repeating the same red line.
func TestBuildRepeatCount_ThirdIdenticalFailureReportsThree(t *testing.T) {
	pool := testSourceArchivePool(t)
	ctx := context.Background()
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "acme")
	seedArchiveGitRepo(t, pool, projectID, envID, appName, "s3://test-bucket/source-uploads/repeat.tar.gz")

	var gitRepoID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM git_repos WHERE project_id=$1 AND environment_id=$2 AND app_name=$3`,
		projectID, envID, appName,
	).Scan(&gitRepoID); err != nil {
		t.Fatalf("lookup git_repos row: %v", err)
	}

	base := time.Now().Add(-time.Hour).UTC()
	const failReason = "dockerfile_build_failed"
	const errMsg = "dockerfile_build_failed: npm install exited 1"

	seedFailedBuildAt(t, pool, gitRepoID, envID, appName, "repeat-sha-1", base, failReason, errMsg)
	seedFailedBuildAt(t, pool, gitRepoID, envID, appName, "repeat-sha-2", base.Add(2*time.Minute), failReason, errMsg)
	thirdID := seedFailedBuildAt(t, pool, gitRepoID, envID, appName, "repeat-sha-3", base.Add(4*time.Minute), failReason, errMsg)

	h := &Handler{pool: pool}
	claims := godClaims(seedUser(t, pool))
	c, rec := newGetBuildCtx(projectID, thirdID, claims)
	h.GetBuild(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		Build struct {
			RepeatCount int `json:"repeat_count"`
		} `json:"build"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if resp.Build.RepeatCount != 3 {
		t.Fatalf("repeat_count=%d, want 3 for the third identical failure", resp.Build.RepeatCount)
	}
}

// TestBuildRepeatCount_ListBuildsAnnotatesWithoutNPlusOne checks the list
// endpoint carries the same signal as GetBuild, computed from the batch it
// already fetched (annotateBuildRepeatCounts), and that a different failure
// breaking the streak resets the count for the newer build.
func TestBuildRepeatCount_ListBuildsAnnotatesWithoutNPlusOne(t *testing.T) {
	pool := testSourceArchivePool(t)
	ctx := context.Background()
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "acme")
	seedArchiveGitRepo(t, pool, projectID, envID, appName, "s3://test-bucket/source-uploads/repeat-list.tar.gz")

	var gitRepoID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM git_repos WHERE project_id=$1 AND environment_id=$2 AND app_name=$3`,
		projectID, envID, appName,
	).Scan(&gitRepoID); err != nil {
		t.Fatalf("lookup git_repos row: %v", err)
	}

	base := time.Now().Add(-time.Hour).UTC()
	seedFailedBuildAt(t, pool, gitRepoID, envID, appName, "list-sha-1", base, "dockerfile_build_failed", "dockerfile_build_failed: npm install exited 1")
	seedFailedBuildAt(t, pool, gitRepoID, envID, appName, "list-sha-2", base.Add(2*time.Minute), "git_auth_failed", "git_auth_failed: could not read Username")

	h := &Handler{pool: pool}
	c, rec := newListBuildsCtx(projectID, envID, appName, godClaims(seedUser(t, pool)))
	h.ListBuilds(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		Builds []struct {
			CommitSHA   string `json:"commit_sha"`
			RepeatCount int    `json:"repeat_count"`
		} `json:"builds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	got := map[string]int{}
	for _, b := range resp.Builds {
		got[b.CommitSHA] = b.RepeatCount
	}
	if got["list-sha-2"] != 1 {
		t.Fatalf("list-sha-2 repeat_count=%d, want 1 (different fail_reason from its predecessor)", got["list-sha-2"])
	}
	if got["list-sha-1"] != 1 {
		t.Fatalf("list-sha-1 repeat_count=%d, want 1 (first failure)", got["list-sha-1"])
	}
}
