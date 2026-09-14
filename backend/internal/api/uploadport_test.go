package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TestResolveUploadPort_WorkerTrue_IgnoresPort pins that worker=true wins
// outright: the user ticking "this is a worker" is the whole statement, and a
// leftover/garbage port value in the same request body (e.g. from a form that
// was previously in port mode) must not leak through or be validated.
func TestResolveUploadPort_WorkerTrue_IgnoresPort(t *testing.T) {
	badPort := -1
	port, worker, errCode, errMsg := resolveUploadPort(setUploadPortRequest{Worker: true, Port: &badPort})
	if errCode != "" {
		t.Fatalf("errCode = %q, want empty; msg=%q", errCode, errMsg)
	}
	if !worker {
		t.Fatalf("worker = false, want true")
	}
	if port != 0 {
		t.Fatalf("port = %d, want 0", port)
	}
}

// TestResolveUploadPort_ValidPort_ClearsWorker proves a valid port in
// 1..65535 resolves to worker=false, mirroring UpdateAppPort's "typing a port
// asserts this app serves HTTP" rule (apps.go:1637-1645) at the pre-deploy
// stage.
func TestResolveUploadPort_ValidPort_ClearsWorker(t *testing.T) {
	p := 3000
	port, worker, errCode, errMsg := resolveUploadPort(setUploadPortRequest{Port: &p})
	if errCode != "" {
		t.Fatalf("errCode = %q, want empty; msg=%q", errCode, errMsg)
	}
	if worker {
		t.Fatalf("worker = true, want false")
	}
	if port != 3000 {
		t.Fatalf("port = %d, want 3000", port)
	}
}

// TestResolveUploadPort_OutOfRange_RejectedWithInvalidPortCode pins the
// validation contract the frontend branches on by err.code, not error prose,
// exactly like UpdateAppPort's own out-of-range test
// (TestUpdateAppPort_OutOfRange_Rejected) and linkGitRepo's range check
// (gitrepos.go:1459-1461): an out-of-range port must fail with code
// "invalid_port" so existing frontend code==='invalid_port' handling covers
// this endpoint too.
func TestResolveUploadPort_OutOfRange_RejectedWithInvalidPortCode(t *testing.T) {
	cases := []int{0, -1, 70000, 65536}
	for _, p := range cases {
		port := p
		_, _, errCode, errMsg := resolveUploadPort(setUploadPortRequest{Port: &port})
		if errCode != "invalid_port" {
			t.Fatalf("port=%d: errCode = %q, want invalid_port", p, errCode)
		}
		if errMsg == "" {
			t.Fatalf("port=%d: errMsg empty, want a range explanation", p)
		}
	}
}

// TestResolveUploadPort_MissingPort_RejectedWithInvalidPortCode proves a
// request with neither worker=true nor a port is rejected the same way an
// out-of-range port is, rather than silently defaulting to some port.
func TestResolveUploadPort_MissingPort_RejectedWithInvalidPortCode(t *testing.T) {
	_, _, errCode, _ := resolveUploadPort(setUploadPortRequest{})
	if errCode != "invalid_port" {
		t.Fatalf("errCode = %q, want invalid_port", errCode)
	}
}

// TestResolveUploadPort_BoundaryPortsAccepted proves the inclusive range
// endpoints 1 and 65535 are both valid, matching UpdateAppPort's own
// boundary (apps.go:1703).
func TestResolveUploadPort_BoundaryPortsAccepted(t *testing.T) {
	for _, p := range []int{1, 65535} {
		port := p
		got, worker, errCode, errMsg := resolveUploadPort(setUploadPortRequest{Port: &port})
		if errCode != "" {
			t.Fatalf("port=%d: errCode = %q, want empty; msg=%q", p, errCode, errMsg)
		}
		if worker {
			t.Fatalf("port=%d: worker = true, want false", p)
		}
		if got != p {
			t.Fatalf("port=%d: resolved port = %d", p, got)
		}
	}
}

// TestSetUploadPort_ValidPort_WritesGitReposRow is the regression gate for the
// pre-deploy correction window: an uploaded archive whose detection guessed
// wrong must be fixable by writing a port straight onto the provider='archive'
// git_repos row (isWorkerUpload's target, uploadsource.go:38-52) before the
// app has ever been materialized by HandoffDeploy.
func TestSetUploadPort_ValidPort_WritesGitReposRow(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	appName := "upload-" + uuid.NewString()[:8]
	seedArchiveGitRepo(t, pool, projectID, envID, appName, "s3://bucket/key.zip")
	if _, err := pool.Exec(context.Background(),
		`UPDATE git_repos SET worker = TRUE, port = 8080 WHERE project_id = $1 AND environment_id = $2 AND app_name = $3`,
		projectID, envID, appName,
	); err != nil {
		t.Fatalf("seed worker state: %v", err)
	}

	c, rec := newCreateCtx(t, `{"port":3000}`,
		gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
		}, claims)
	h.SetUploadPort(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var port int
	var worker bool
	if err := pool.QueryRow(context.Background(),
		`SELECT port, worker FROM git_repos WHERE project_id = $1 AND environment_id = $2 AND app_name = $3`,
		projectID, envID, appName,
	).Scan(&port, &worker); err != nil {
		t.Fatalf("read back git_repos: %v", err)
	}
	if port != 3000 {
		t.Fatalf("git_repos.port = %d, want 3000", port)
	}
	if worker {
		t.Fatalf("git_repos.worker = true, want false (a typed port clears it)")
	}
}

// TestSetUploadPort_WorkerTrue_SetsWorkerFlag proves the other direction: an
// archive detection guessed had a web port, but the user knows it is actually
// a bot/queue consumer, can confirm worker=true before the first build
// deploys and reaches HandoffDeploy's !repo.Worker domain gate
// (build-agent/internal/db/deploy.go:280).
func TestSetUploadPort_WorkerTrue_SetsWorkerFlag(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	appName := "upload-" + uuid.NewString()[:8]
	seedArchiveGitRepo(t, pool, projectID, envID, appName, "s3://bucket/key2.zip")

	c, rec := newCreateCtx(t, `{"worker":true}`,
		gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
		}, claims)
	h.SetUploadPort(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var worker bool
	if err := pool.QueryRow(context.Background(),
		`SELECT worker FROM git_repos WHERE project_id = $1 AND environment_id = $2 AND app_name = $3`,
		projectID, envID, appName,
	).Scan(&worker); err != nil {
		t.Fatalf("read back git_repos: %v", err)
	}
	if !worker {
		t.Fatalf("git_repos.worker = false, want true")
	}
}

// TestSetUploadPort_GitLinkedRepo_NotFound proves this endpoint can never be
// used to rewrite a git-linked repo's port/worker fields, the same guarantee
// UploadSourceArchive's own UPSERT gives the provider column
// (uploadsource.go:241-247): a provider != 'archive' row must 404, not update.
func TestSetUploadPort_GitLinkedRepo_NotFound(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	appName := "gitlinked-" + uuid.NewString()[:8]
	seedGitHubRepo(t, pool, projectID, envID, appName, "acme/"+appName)

	c, rec := newCreateCtx(t, `{"port":3000}`,
		gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
		}, claims)
	h.SetUploadPort(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}

	var provider string
	var port int
	if err := pool.QueryRow(context.Background(),
		`SELECT provider, port FROM git_repos WHERE project_id = $1 AND environment_id = $2 AND app_name = $3`,
		projectID, envID, appName,
	).Scan(&provider, &port); err != nil {
		t.Fatalf("read back git_repos: %v", err)
	}
	if provider != "github" {
		t.Fatalf("provider = %q, want github (must be untouched)", provider)
	}
}

// TestSetUploadPort_NoArchiveRow_NotFound proves the endpoint 404s cleanly
// when no upload has happened for this app yet, rather than creating a row
// out of thin air.
func TestSetUploadPort_NoArchiveRow_NotFound(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	appName := "nosuchapp-" + uuid.NewString()[:8]

	c, rec := newCreateCtx(t, `{"port":3000}`,
		gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
		}, claims)
	h.SetUploadPort(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestSetUploadPort_OutOfRangePort_Rejected proves the handler surfaces
// resolveUploadPort's invalid_port code end-to-end and never touches the row.
func TestSetUploadPort_OutOfRangePort_Rejected(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	appName := "badport-" + uuid.NewString()[:8]
	seedArchiveGitRepo(t, pool, projectID, envID, appName, "s3://bucket/key3.zip")

	c, rec := newCreateCtx(t, `{"port":70000}`,
		gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
		}, claims)
	h.SetUploadPort(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if want := `"code":"invalid_port"`; !contains(rec.Body.String(), want) {
		t.Fatalf("body = %s, want it to contain %s", rec.Body.String(), want)
	}
}

// TestSetUploadPort_RewritesInFlightBuildArchivePort is the regression gate for
// the clobber this endpoint exists to survive. Writing git_repos alone is not
// enough: for an archive build the build-agent overlays the app's row per build
// and puts builds.archive_port back on top of it
// (sourceForBuild, build-agent/internal/worker/runner.go:1892-1893), and
// UploadSourceArchive stores the nominal 8080 there for a portless upload
// (uploadsource.go:236-239). Without this write the user types a port, the
// console reports success, and the still-running build deploys 8080 anyway.
// Only a build that has not yet handed off is touched; a terminal build is the
// record of a deploy that already happened and must not be rewritten.
func TestSetUploadPort_RewritesInFlightBuildArchivePort(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool}
	projectID, envID := seedOptimisticFixture(t, pool)
	userID := seedUser(t, pool)
	claims := godClaims(userID)
	appName := "upload-" + uuid.NewString()[:8]
	seedArchiveGitRepo(t, pool, projectID, envID, appName, "s3://bucket/key.zip")

	var gitRepoID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM git_repos WHERE project_id = $1 AND environment_id = $2 AND app_name = $3`,
		projectID, envID, appName,
	).Scan(&gitRepoID); err != nil {
		t.Fatalf("read git_repo id: %v", err)
	}

	var runningBuildID, doneBuildID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO builds (git_repo_id, environment_id, app_name, commit_sha, branch, triggered_by, trigger, status, archive_url, archive_framework, archive_port)
		 VALUES ($1, $2, $3, $4, 'upload', $5, 'manual', 'building', 's3://bucket/key.zip', 'node', 8080)
		 RETURNING id`,
		gitRepoID, envID, appName, "manual-"+uuid.NewString()[:12], userID,
	).Scan(&runningBuildID); err != nil {
		t.Fatalf("seed in-flight build: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO builds (git_repo_id, environment_id, app_name, commit_sha, branch, triggered_by, trigger, status, archive_url, archive_framework, archive_port)
		 VALUES ($1, $2, $3, $4, 'upload', $5, 'manual', 'success', 's3://bucket/old.zip', 'node', 8080)
		 RETURNING id`,
		gitRepoID, envID, appName, "manual-"+uuid.NewString()[:12], userID,
	).Scan(&doneBuildID); err != nil {
		t.Fatalf("seed finished build: %v", err)
	}

	c, rec := newCreateCtx(t, `{"port":3000}`,
		gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
		}, claims)
	h.SetUploadPort(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var runningPort, donePort int
	if err := pool.QueryRow(context.Background(),
		`SELECT archive_port FROM builds WHERE id = $1`, runningBuildID,
	).Scan(&runningPort); err != nil {
		t.Fatalf("read in-flight build: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT archive_port FROM builds WHERE id = $1`, doneBuildID,
	).Scan(&donePort); err != nil {
		t.Fatalf("read finished build: %v", err)
	}
	if runningPort != 3000 {
		t.Fatalf("in-flight builds.archive_port = %d, want 3000 (sourceForBuild would clobber git_repos.port with it)", runningPort)
	}
	if donePort != 8080 {
		t.Fatalf("finished builds.archive_port = %d, want 8080 (a terminal build must not be rewritten)", donePort)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i+len(substr) <= len(s); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}
