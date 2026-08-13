package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func newListBuildsCtx(projectID, envID uuid.UUID, appName string, claims *auth.Claims) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	path := "/api/v1/projects/" + projectID.String() + "/environments/" + envID.String() + "/apps/" + appName + "/builds"
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "envId", Value: envID.String()},
		{Key: "appName", Value: appName},
	}
	auth.SetClaims(c, claims)
	return c, rec
}

// TestBuildHistorySurvivesSourceRemoval pins the contract migration 116 buys:
// dropping the git_repos row (disconnect, re-upload, app teardown) must detach
// the build, not delete it. Before 116 the FK cascaded, so a user who
// disconnected a repo lost every record of why their earlier builds failed,
// and our own funnel numbers silently lost their denominator -- measured on
// prod 2026-08-13, 20 of 26 build ids named by BuildFinished audit rows no
// longer had a build row at all.
func TestBuildHistorySurvivesSourceRemoval(t *testing.T) {
	pool := testSourceArchivePool(t)
	ctx := context.Background()
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "acme")
	seedArchiveGitRepo(t, pool, projectID, envID, appName, "s3://test-bucket/source-uploads/x.tar.gz")

	var gitRepoID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM git_repos WHERE project_id=$1 AND environment_id=$2 AND app_name=$3`,
		projectID, envID, appName,
	).Scan(&gitRepoID); err != nil {
		t.Fatalf("lookup git_repos row: %v", err)
	}

	var buildID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO builds (git_repo_id, environment_id, app_name, commit_sha, branch, trigger, status, fail_reason)
		 VALUES ($1, $2, $3, 'manual-116', 'upload', 'manual', 'failed', 'no_dockerfile')
		 RETURNING id`,
		gitRepoID, envID, appName,
	).Scan(&buildID); err != nil {
		t.Fatalf("seed build: %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM git_repos WHERE id=$1`, gitRepoID); err != nil {
		t.Fatalf("delete git_repos row: %v", err)
	}

	var survivingRepoID *uuid.UUID
	var failReason *string
	if err := pool.QueryRow(ctx,
		`SELECT git_repo_id, fail_reason FROM builds WHERE id=$1`, buildID,
	).Scan(&survivingRepoID, &failReason); err != nil {
		t.Fatalf("build row did not survive removal of its source: %v", err)
	}
	if survivingRepoID != nil {
		t.Fatalf("git_repo_id=%v, want NULL after the source row was deleted", *survivingRepoID)
	}
	if failReason == nil || *failReason != "no_dockerfile" {
		t.Fatalf("fail_reason=%v, want the original verdict to survive", failReason)
	}

	h := &Handler{pool: pool}
	c, rec := newListBuildsCtx(projectID, envID, appName, godClaims(seedUser(t, pool)))
	h.ListBuilds(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}

	var body struct {
		Builds []build `json:"builds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Builds) != 1 {
		t.Fatalf("builds=%d, want the detached build still listed for the app", len(body.Builds))
	}
	if body.Builds[0].ID != buildID {
		t.Fatalf("build id=%s, want %s", body.Builds[0].ID, buildID)
	}
	if body.Builds[0].Source != "" {
		t.Fatalf("source=%q, want empty: the source row is gone and must not be invented", body.Builds[0].Source)
	}
}
