package api

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// buildTestArchive returns bytes of a minimal, valid zip archive: enough for
// sourcedetect.Detect to succeed without matching any known framework.
func buildTestArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("README.md")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// fakeSourceUploader is an in-memory SourceUploader stand-in so the reupload
// test never dials a real object store: it only needs to prove the DB side
// (git_repos upsert + queued build), not minio wiring, which
// TestDownloadSourceArchive_HappyPath already exercises against a real
// endpoint contract.
type fakeSourceUploader struct {
	bucket  string
	puts    int
	objects map[string][]byte
}

func (f *fakeSourceUploader) Enabled() bool  { return true }
func (f *fakeSourceUploader) Bucket() string { return f.bucket }
func (f *fakeSourceUploader) PutObject(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	f.puts++
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if f.objects == nil {
		f.objects = map[string][]byte{}
	}
	f.objects[key] = data
	return nil
}
func (f *fakeSourceUploader) PresignGet(ctx context.Context, key, filename string, ttl time.Duration) (string, error) {
	return "https://example.invalid/" + key, nil
}
func (f *fakeSourceUploader) GetObject(ctx context.Context, key string, maxBytes int64) ([]byte, error) {
	data, ok := f.objects[key]
	if !ok {
		return nil, fmt.Errorf("no such object %q", key)
	}
	return data, nil
}

func newUploadSourceArchiveCtx(t *testing.T, projectID, envID uuid.UUID, appName string, claims *auth.Claims, archive []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("archive", "source.zip")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(archive); err != nil {
		t.Fatalf("write archive bytes: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	path := "/api/v1/projects/" + projectID.String() + "/environments/" + envID.String() + "/apps/" + appName + "/source-archive"
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	c.Request = req
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "envId", Value: envID.String()},
		{Key: "appName", Value: appName},
	}
	auth.SetClaims(c, claims)
	return c, rec
}

func countBuilds(t *testing.T, pool *pgxpool.Pool, gitRepoID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM builds WHERE git_repo_id = $1`, gitRepoID,
	).Scan(&n); err != nil {
		t.Fatalf("count builds: %v", err)
	}
	return n
}

// TestUploadSourceArchive_Reupload_UpdatesRepoAndQueuesBuild covers artempro2021's
// "как обновить архив": an app already deployed from an uploaded archive
// (git_repos.provider='archive' row already exists) gets a second archive
// uploaded through the same endpoint. It must update the existing git_repos
// row in place (new clone_url, installation_id cleared) rather than error or
// duplicate it, and it must enqueue a second build row.
func TestUploadSourceArchive_Reupload_UpdatesRepoAndQueuesBuild(t *testing.T) {
	pool := testSourceArchivePool(t)
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "acme")
	oldCloneURL := "s3://test-bucket/source-uploads/" + projectID.String() + "/" + appName + "/original.zip"
	seedArchiveGitRepo(t, pool, projectID, envID, appName, oldCloneURL)

	var gitRepoID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM git_repos WHERE project_id=$1 AND environment_id=$2 AND app_name=$3`,
		projectID, envID, appName,
	).Scan(&gitRepoID); err != nil {
		t.Fatalf("lookup seeded git_repos row: %v", err)
	}
	if got := countBuilds(t, pool, gitRepoID); got != 0 {
		t.Fatalf("builds before reupload = %d, want 0", got)
	}

	uploader := &fakeSourceUploader{bucket: "test-bucket"}
	h := &Handler{pool: pool, sourceUploader: uploader}
	claims := &auth.Claims{UserID: seedUser(t, pool), Groups: []string{"/platform-admins"}}
	c, rec := newUploadSourceArchiveCtx(t, projectID, envID, appName, claims, buildTestArchive(t))
	h.UploadSourceArchive(c)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s want 202", rec.Code, rec.Body.String())
	}
	if uploader.puts != 1 {
		t.Fatalf("PutObject calls = %d, want 1", uploader.puts)
	}

	var newCloneURL string
	var installationID *uuid.UUID
	var repoCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT clone_url, installation_id FROM git_repos WHERE id = $1`, gitRepoID,
	).Scan(&newCloneURL, &installationID); err != nil {
		t.Fatalf("read updated git_repos row: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM git_repos WHERE project_id=$1 AND environment_id=$2 AND app_name=$3`,
		projectID, envID, appName,
	).Scan(&repoCount); err != nil {
		t.Fatalf("count git_repos rows: %v", err)
	}

	if repoCount != 1 {
		t.Fatalf("git_repos rows for app = %d, want 1 (must UPDATE, not duplicate)", repoCount)
	}
	if newCloneURL == oldCloneURL {
		t.Fatalf("clone_url unchanged after reupload: still %q", newCloneURL)
	}
	if installationID != nil {
		t.Fatalf("installation_id = %v, want NULL after reupload", *installationID)
	}
	if got := countBuilds(t, pool, gitRepoID); got != 1 {
		t.Fatalf("builds after reupload = %d, want 1 new build row", got)
	}
}

// TestUploadSourceArchive_InsufficientRole_Forbidden mirrors the download
// endpoint's role gate: a read-only member may not push a new archive over
// an app's source any more than they can trigger any other write.
func TestUploadSourceArchive_InsufficientRole_Forbidden(t *testing.T) {
	pool := testSourceArchivePool(t)
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "acme")
	seedArchiveGitRepo(t, pool, projectID, envID, appName, "s3://test-bucket/source-uploads/"+projectID.String()+"/"+appName+"/original.zip")

	uploader := &fakeSourceUploader{bucket: "test-bucket"}
	h := &Handler{pool: pool, sourceUploader: uploader}
	claims := &auth.Claims{UserID: uuid.New(), Groups: []string{"/orgs/acme/projects/" + projectID.String() + "/ReadOnly"}}
	c, rec := newUploadSourceArchiveCtx(t, projectID, envID, appName, claims, buildTestArchive(t))
	h.UploadSourceArchive(c)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d body=%s want 403", rec.Code, rec.Body.String())
	}
	if uploader.puts != 0 {
		t.Fatalf("PutObject calls = %d, want 0 (should be forbidden before storage write)", uploader.puts)
	}
}

// seedGitHubRepo seeds the row an app gets when it is connected to GitHub: a
// real repo full name, an https clone URL and a production branch that pushes
// are matched against by the build-agent webhook.
func seedGitHubRepo(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName, fullName string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO git_repos (project_id, environment_id, app_name, provider, repo_full_name, clone_url, production_branch, auto_deploy)
		 VALUES ($1, $2, $3, 'github', $4, $5, 'main', true)`,
		projectID, envID, appName, fullName, "https://github.com/"+fullName+".git",
	); err != nil {
		t.Fatalf("seed github git_repos: %v", err)
	}
}

// TestUploadSourceArchive_GitLinkedApp_KeepsGitBinding locks the fix for the
// keksmd/family-tree incident (2026-08-14): uploading an archive for an app
// that is connected to GitHub used to rewrite its single git_repos row to
// provider='archive' / repo_full_name='upload/<app>' / production_branch='upload'
// and NULL the installation. That is a silent one-way door - every later push
// reached the webhook, resolved to no repo and was dropped, so auto deploy died
// for good because someone uploaded a folder once (or because a ddc deploy fell
// back to an archive for a few transient seconds).
//
// The archive must ride on the build row instead, leaving the app's source
// binding describing where the code actually lives.
func TestUploadSourceArchive_GitLinkedApp_KeepsGitBinding(t *testing.T) {
	pool := testSourceArchivePool(t)
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "acme")
	seedGitHubRepo(t, pool, projectID, envID, appName, "keksmd/family-tree")

	uploader := &fakeSourceUploader{bucket: "test-bucket"}
	h := &Handler{pool: pool, sourceUploader: uploader}
	claims := &auth.Claims{UserID: seedUser(t, pool), Groups: []string{"/platform-admins"}}
	c, rec := newUploadSourceArchiveCtx(t, projectID, envID, appName, claims, buildTestArchive(t))
	h.UploadSourceArchive(c)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s want 202", rec.Code, rec.Body.String())
	}

	var provider, fullName, cloneURL, branch string
	var autoDeploy bool
	if err := pool.QueryRow(context.Background(),
		`SELECT provider, repo_full_name, clone_url, production_branch, auto_deploy
		   FROM git_repos WHERE project_id=$1 AND environment_id=$2 AND app_name=$3`,
		projectID, envID, appName,
	).Scan(&provider, &fullName, &cloneURL, &branch, &autoDeploy); err != nil {
		t.Fatalf("read git_repos row: %v", err)
	}
	if provider != "github" {
		t.Fatalf("provider = %q, want github (upload must not unbind the app from git)", provider)
	}
	if fullName != "keksmd/family-tree" {
		t.Fatalf("repo_full_name = %q, want keksmd/family-tree", fullName)
	}
	if cloneURL != "https://github.com/keksmd/family-tree.git" {
		t.Fatalf("clone_url = %q, want the github clone url", cloneURL)
	}
	if branch != "main" {
		t.Fatalf("production_branch = %q, want main (pushes must still match)", branch)
	}
	if !autoDeploy {
		t.Fatalf("auto_deploy = false, want true")
	}

	var archiveURL string
	var archivePort int
	if err := pool.QueryRow(context.Background(),
		`SELECT archive_url, archive_port FROM builds
		  WHERE environment_id=$1 AND app_name=$2 ORDER BY created_at DESC LIMIT 1`,
		envID, appName,
	).Scan(&archiveURL, &archivePort); err != nil {
		t.Fatalf("read queued build: %v", err)
	}
	if archiveURL == "" {
		t.Fatalf("build.archive_url is empty; the build has no way to find the uploaded source")
	}
	if archivePort <= 0 {
		t.Fatalf("build.archive_port = %d, want the detected port", archivePort)
	}
}
