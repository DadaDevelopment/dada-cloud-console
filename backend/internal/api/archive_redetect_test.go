package api

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/auth"
)

// buildPythonScriptArchive returns a zip of the shape a live user uploaded on
// 2026-08-13: python sources at the archive root and no manifest at all.
func buildPythonScriptArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"agent.py", "serve.py", "README.md"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte("print('hi')\n")); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func repoFramework(t *testing.T, pool *pgxpool.Pool, gitRepoID uuid.UUID) *string {
	t.Helper()
	var framework *string
	if err := pool.QueryRow(context.Background(),
		`SELECT framework_override FROM git_repos WHERE id = $1`, gitRepoID,
	).Scan(&framework); err != nil {
		t.Fatalf("read framework_override: %v", err)
	}
	return framework
}

func seedArchiveRepoWithObject(t *testing.T, pool *pgxpool.Pool, uploader *fakeSourceUploader, projectID, envID uuid.UUID, appName string, archive []byte) (uuid.UUID, string) {
	t.Helper()
	key := "source-uploads/" + projectID.String() + "/" + appName + "/" + uuid.New().String() + ".zip"
	if uploader.objects == nil {
		uploader.objects = map[string][]byte{}
	}
	uploader.objects[key] = archive
	cloneURL := "s3://" + uploader.bucket + "/" + key
	seedArchiveGitRepo(t, pool, projectID, envID, appName, cloneURL)

	var gitRepoID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM git_repos WHERE project_id=$1 AND environment_id=$2 AND app_name=$3`,
		projectID, envID, appName,
	).Scan(&gitRepoID); err != nil {
		t.Fatalf("lookup seeded git_repos row: %v", err)
	}
	return gitRepoID, cloneURL
}

// TestTriggerBuild_RedetectsFrameworkForUndetectedArchive is the live case
// behind 770a5197: the archive was uploaded while the detector still refused
// manifest-less python, so framework_override stayed NULL and every rebuild
// repeated "no_dockerfile" even after the detector learned that shape. A
// rebuild must re-ask.
func TestTriggerBuild_RedetectsFrameworkForUndetectedArchive(t *testing.T) {
	pool := testSourceArchivePool(t)
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "acme")
	uploader := &fakeSourceUploader{bucket: "test-bucket"}
	gitRepoID, _ := seedArchiveRepoWithObject(t, pool, uploader, projectID, envID, appName, buildPythonScriptArchive(t))

	if got := repoFramework(t, pool, gitRepoID); got != nil {
		t.Fatalf("seeded framework_override = %q, want NULL", *got)
	}

	h := &Handler{pool: pool, sourceUploader: uploader}
	claims := &auth.Claims{UserID: seedUser(t, pool), Groups: []string{"/platform-admins"}}
	c, rec := newTriggerBuildCtx(projectID, envID, appName, claims)
	h.TriggerBuild(c)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s want 202", rec.Code, rec.Body.String())
	}
	got := repoFramework(t, pool, gitRepoID)
	if got == nil || *got != "python" {
		t.Fatalf("framework_override = %v, want python (rebuild must re-detect)", got)
	}
	if n := countBuilds(t, pool, gitRepoID); n != 1 {
		t.Fatalf("builds = %d, want 1 (re-detection must not swallow the build)", n)
	}
}

// TestTriggerBuild_KeepsExistingFrameworkAndSurvivesMissingArchive pins the two
// ways re-detection must stay out of the way: a framework someone already
// chose is never overwritten, and an archive we cannot read still queues the
// build instead of failing the user's rebuild.
func TestTriggerBuild_KeepsExistingFrameworkAndSurvivesMissingArchive(t *testing.T) {
	pool := testSourceArchivePool(t)
	uploader := &fakeSourceUploader{bucket: "test-bucket"}
	claims := &auth.Claims{UserID: seedUser(t, pool), Groups: []string{"/platform-admins"}}

	projectID, envID, appName := seedSourceArchiveProject(t, pool, "acme")
	chosenRepo, _ := seedArchiveRepoWithObject(t, pool, uploader, projectID, envID, appName, buildPythonScriptArchive(t))
	if _, err := pool.Exec(context.Background(),
		`UPDATE git_repos SET framework_override = 'node' WHERE id = $1`, chosenRepo); err != nil {
		t.Fatalf("set framework_override: %v", err)
	}
	c, rec := newTriggerBuildCtx(projectID, envID, appName, claims)
	h := &Handler{pool: pool, sourceUploader: uploader}
	h.TriggerBuild(c)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s want 202", rec.Code, rec.Body.String())
	}
	if got := repoFramework(t, pool, chosenRepo); got == nil || *got != "node" {
		t.Fatalf("framework_override = %v, want node (an existing choice is never overwritten)", got)
	}

	goneProject, goneEnv, goneApp := seedSourceArchiveProject(t, pool, "acme")
	goneRepo, _ := seedArchiveRepoWithObject(t, pool, uploader, goneProject, goneEnv, goneApp, buildPythonScriptArchive(t))
	uploader.objects = map[string][]byte{}
	c2, rec2 := newTriggerBuildCtx(goneProject, goneEnv, goneApp, claims)
	h.TriggerBuild(c2)
	if rec2.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s want 202 (unreadable archive must not fail the rebuild)", rec2.Code, rec2.Body.String())
	}
	if got := repoFramework(t, pool, goneRepo); got != nil {
		t.Fatalf("framework_override = %q, want NULL", *got)
	}
	if n := countBuilds(t, pool, goneRepo); n != 1 {
		t.Fatalf("builds = %d, want 1", n)
	}
}
