package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
)

// writeTree materialises path/content pairs under root, creating parents.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

func TestStrayAppManifests_FindsRenamedProjectPath(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"clusters/beget-prod/projects/old-slug/environments/prod/apps/web/app.yaml":    "kind: Application\n",
		"clusters/beget-prod/projects/old-slug/environments/prod/apps/web/values.yaml": "image: x\n",
		"clusters/beget-prod/projects/other/environments/prod/apps/api/app.yaml":       "kind: Application\n",
	})

	got, err := strayAppManifests(root, "web")
	if err != nil {
		t.Fatalf("strayAppManifests: %v", err)
	}
	want := []string{"clusters/beget-prod/projects/old-slug/environments/prod/apps/web/app.yaml"}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestStrayAppManifests_CleanTree(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"clusters/beget-prod/projects/other/environments/prod/apps/api/app.yaml": "kind: Application\n",
	})

	got, err := strayAppManifests(root, "web")
	if err != nil {
		t.Fatalf("strayAppManifests: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none -- a deleted app must stay deletable, not wedge on a false positive", got)
	}
}

// TestStrayAppManifests_IgnoresLookalikePaths keeps the guard from firing on
// files that ArgoCD never renders as this app: an app.yaml nested deeper inside
// the app folder (source of the user's own chart), and a same-named directory
// that is not an entry under apps/.
func TestStrayAppManifests_IgnoresLookalikePaths(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"clusters/beget-prod/projects/p/environments/prod/apps/web/chart/app.yaml": "nested: true\n",
		"clusters/beget-prod/projects/web/app.yaml":                                "kind: Project\n",
	})

	got, err := strayAppManifests(root, "web")
	if err != nil {
		t.Fatalf("strayAppManifests: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

// TestStrayAppManifests_SkipsGitDir guards against the .git object store: a
// packed blob or a stale index copy under .git must never be read as a live
// manifest, or every delete in a repo that ever hosted the app would fail.
func TestStrayAppManifests_SkipsGitDir(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".git/modules/x/clusters/beget-prod/projects/p/environments/prod/apps/web/app.yaml": "kind: Application\n",
	})

	got, err := strayAppManifests(root, "web")
	if err != nil {
		t.Fatalf("strayAppManifests: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestVerifyDeleteRemovedApp(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"clusters/beget-prod/projects/old-slug/environments/prod/apps/web/app.yaml": "kind: Application\n",
	})

	if err := verifyDeleteRemovedApp(root, "gone", "clusters/x/app.yaml"); err != nil {
		t.Errorf("app with no manifests must be deletable, got %v", err)
	}

	err := verifyDeleteRemovedApp(root, "web", "clusters/x/app.yaml")
	if err == nil {
		t.Fatal("a still-deployed app must not be reported as deleted")
	}
	if !strings.Contains(err.Error(), "old-slug") {
		t.Errorf("error must name the surviving manifest so the operator can find it, got %q", err)
	}
}

// newAppManifestRepo seeds a bare remote carrying app manifests at the given
// repo-relative paths and returns a Manager cloned from it.
func newAppManifestRepo(t *testing.T, paths ...string) *git.Manager {
	t.Helper()

	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	if _, err := gogit.PlainInit(remoteDir, true); err != nil {
		t.Fatalf("init bare remote: %v", err)
	}
	seedDir := filepath.Join(t.TempDir(), "seed")
	seedRepo, err := gogit.PlainInit(seedDir, false)
	if err != nil {
		t.Fatalf("init seed repo: %v", err)
	}
	wt, err := seedRepo.Worktree()
	if err != nil {
		t.Fatalf("seed worktree: %v", err)
	}
	if _, err := seedRepo.CreateRemote(&gogitconfig.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteDir},
	}); err != nil {
		t.Fatalf("create remote: %v", err)
	}
	for _, p := range paths {
		historyRewriteWriteAndAdd(t, seedDir, wt, p, "kind: Application\n")
	}
	if _, err := wt.Commit("seed app manifests", &gogit.CommitOptions{Author: historyRewriteSig()}); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
	historyRewritePush(t, seedRepo, false)

	mgr := git.New(git.RepoConfig{
		RepoURL:   remoteDir,
		Branch:    historyRewriteTestBranch,
		LocalBase: t.TempDir(),
	})
	if err := mgr.EnsureCloned(); err != nil {
		t.Fatalf("clone: %v", err)
	}
	return mgr
}

// seedDeleteAppOperation inserts a Processing DeleteApp operation for appName.
func seedDeleteAppOperation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID, environmentID uuid.UUID, appName string) db.Operation {
	t.Helper()

	var actorID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM users ORDER BY created_at DESC LIMIT 1`,
	).Scan(&actorID); err != nil {
		t.Fatalf("pick actor: %v", err)
	}

	opID := uuid.New()
	payload, err := json.Marshal(map[string]string{"name": appName})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	exec(t, ctx, pool,
		`INSERT INTO operations (id, actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, $4, 'DeleteApp', 'App', $5, 'Processing', $6)`,
		opID, actorID, projectID, environmentID, appName, payload)

	return db.Operation{
		ID:            opID,
		ProjectID:     projectID,
		EnvironmentID: &environmentID,
		Action:        "DeleteApp",
		Payload:       payload,
	}
}

// TestDoDeleteApp_SurvivingManifestFailsOperation is the regression for the
// prod defect: a DeleteApp whose computed paths hold nothing pushes nothing,
// and the operation used to be marked Committed with an empty git_commit while
// ArgoCD kept the app running. bruzas.85@mail.ru hit that on tvk-assistantbot
// and pressed delete three times over eleven hours, each press "succeeding".
// doDeleteApp must return an error instead, so poll marks the operation Failed
// and the console shows the user a real fault.
func TestDoDeleteApp_SurvivingManifestFailsOperation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	applyMigrations(t, ctx, pool)

	appName := "tvk-assistantbot"
	projectID, environmentID, _ := seedAppWithGitRepo(t, ctx, pool, appName)
	op := seedDeleteAppOperation(t, ctx, pool, projectID, environmentID, appName)

	strayPath := "clusters/beget-prod/projects/renamed-slug/environments/prod/apps/" + appName + "/app.yaml"
	mgr := newAppManifestRepo(t, strayPath)

	w := newTestDBWatcher(pool)
	w.cfg = &config.Config{DefaultRepoURL: mgr.RepoURL()}
	w.managers = map[string]*git.Manager{mgr.RepoURL(): mgr}

	err = w.doDeleteApp(ctx, op)
	if err == nil {
		t.Fatal("doDeleteApp reported success while the app's manifest is still in git")
	}
	if !strings.Contains(err.Error(), "renamed-slug") {
		t.Errorf("error must name the surviving manifest, got %q", err)
	}

	var status string
	var gitCommit *string
	if err := pool.QueryRow(ctx,
		`SELECT status, git_commit FROM operations WHERE id = $1`, op.ID,
	).Scan(&status, &gitCommit); err != nil {
		t.Fatalf("read operation: %v", err)
	}
	if status == "Committed" {
		t.Errorf("operation status = Committed with git_commit %v; a delete that removed nothing must not read as done", gitCommit)
	}

	var successAudit int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events WHERE operation_id = $1 AND outcome = 'success'`, op.ID,
	).Scan(&successAudit); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if successAudit != 0 {
		t.Errorf("success audit rows = %d, want 0", successAudit)
	}
}

// TestDoDeleteApp_NeverDeployedAppStaysDeletable is the other half: an app that
// was created but never rendered into git has no manifests anywhere, so the
// delete is an honest no-op and must still complete. Failing it would leave
// imported-but-never-built apps permanently undeletable.
func TestDoDeleteApp_NeverDeployedAppStaysDeletable(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	applyMigrations(t, ctx, pool)

	appName := "never-shipped"
	projectID, environmentID, _ := seedAppWithGitRepo(t, ctx, pool, appName)
	op := seedDeleteAppOperation(t, ctx, pool, projectID, environmentID, appName)

	mgr := newAppManifestRepo(t, "clusters/beget-prod/projects/other/environments/prod/apps/api/app.yaml")

	w := newTestDBWatcher(pool)
	w.cfg = &config.Config{DefaultRepoURL: mgr.RepoURL()}
	w.managers = map[string]*git.Manager{mgr.RepoURL(): mgr}

	if err := w.doDeleteApp(ctx, op); err != nil {
		t.Fatalf("doDeleteApp on a never-deployed app: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM operations WHERE id = $1`, op.ID,
	).Scan(&status); err != nil {
		t.Fatalf("read operation: %v", err)
	}
	if status != "Committed" {
		t.Errorf("operation status = %s, want Committed", status)
	}
}
