package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testSourceArchivePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping source-archive-download DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedSourceArchiveProject(t *testing.T, pool *pgxpool.Pool, orgID string) (projectID uuid.UUID, envID uuid.UUID, appName string) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	appName = "app-" + suffix

	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $1, $2) RETURNING id`,
		"source-archive-test-"+suffix, orgID,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, projectID) })

	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "ns-"+suffix,
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json) VALUES ($1, $2, 'App', $3, 'Ready', '{}')`,
		projectID, envID, appName,
	); err != nil {
		t.Fatalf("seed resource_snapshot: %v", err)
	}
	return projectID, envID, appName
}

func seedArchiveGitRepo(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName, cloneURL string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO git_repos (project_id, environment_id, app_name, provider, repo_full_name, clone_url, production_branch)
		 VALUES ($1, $2, $3, 'archive', $4, $5, 'upload')`,
		projectID, envID, appName, "upload/"+appName, cloneURL,
	); err != nil {
		t.Fatalf("seed git_repos: %v", err)
	}
}

func newSourceArchiveDownloadCtx(projectID, envID uuid.UUID, appName string, claims *auth.Claims) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	path := "/api/v1/projects/" + projectID.String() + "/environments/" + envID.String() + "/apps/" + appName + "/source-archive/download"
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "envId", Value: envID.String()},
		{Key: "appName", Value: appName},
	}
	auth.SetClaims(c, claims)
	return c, rec
}

func TestDownloadSourceArchive_HappyPath(t *testing.T) {
	pool := testSourceArchivePool(t)
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "acme")
	seedArchiveGitRepo(t, pool, projectID, envID, appName, "s3://test-bucket/source-uploads/"+projectID.String()+"/"+appName+"/abc123.tar.gz")

	h := &Handler{
		pool:           pool,
		sourceUploader: cloudtask.NewSourceUploader("minio.local:9000", "test-bucket", "us-east-1", "key", "secret", true),
	}
	claims := &auth.Claims{UserID: uuid.New(), Groups: []string{"/platform-admins"}}
	c, rec := newSourceArchiveDownloadCtx(projectID, envID, appName, claims)
	h.DownloadSourceArchive(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
}

func TestDownloadSourceArchive_NoArchive_NotFound(t *testing.T) {
	pool := testSourceArchivePool(t)
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "acme")

	h := &Handler{
		pool:           pool,
		sourceUploader: cloudtask.NewSourceUploader("minio.local:9000", "test-bucket", "us-east-1", "key", "secret", true),
	}
	claims := &auth.Claims{UserID: uuid.New(), Groups: []string{"/platform-admins"}}
	c, rec := newSourceArchiveDownloadCtx(projectID, envID, appName, claims)
	h.DownloadSourceArchive(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d body=%s want 404", rec.Code, rec.Body.String())
	}
}

func TestDownloadSourceArchive_NotConfigured_ServiceUnavailable(t *testing.T) {
	pool := testSourceArchivePool(t)
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "acme")
	seedArchiveGitRepo(t, pool, projectID, envID, appName, "s3://test-bucket/source-uploads/"+projectID.String()+"/"+appName+"/abc123.tar.gz")

	h := &Handler{
		pool:           pool,
		sourceUploader: cloudtask.NewSourceUploader("", "", "", "", "", false),
	}
	claims := &auth.Claims{UserID: uuid.New(), Groups: []string{"/platform-admins"}}
	c, rec := newSourceArchiveDownloadCtx(projectID, envID, appName, claims)
	h.DownloadSourceArchive(c)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d body=%s want 503", rec.Code, rec.Body.String())
	}
}

func TestDownloadSourceArchive_InsufficientRole_Forbidden(t *testing.T) {
	pool := testSourceArchivePool(t)
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "acme")
	seedArchiveGitRepo(t, pool, projectID, envID, appName, "s3://test-bucket/source-uploads/"+projectID.String()+"/"+appName+"/abc123.tar.gz")

	h := &Handler{
		pool:           pool,
		sourceUploader: cloudtask.NewSourceUploader("minio.local:9000", "test-bucket", "us-east-1", "key", "secret", true),
	}
	claims := &auth.Claims{UserID: uuid.New(), Groups: []string{"/orgs/acme/projects/" + projectID.String() + "/ReadOnly"}}
	c, rec := newSourceArchiveDownloadCtx(projectID, envID, appName, claims)
	h.DownloadSourceArchive(c)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d body=%s want 403", rec.Code, rec.Body.String())
	}
}
