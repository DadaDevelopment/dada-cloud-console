package worker

import (
	"context"
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

func TestStrayProjectManifests_CleanTree(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"clusters/beget-prod/projects/other/project.yaml":                        "kind: Project\n",
		"clusters/beget-prod/projects/other/environments/prod/apps/api/app.yaml": "kind: Application\n",
	})

	got, err := strayProjectManifests(root, "gone")
	if err != nil {
		t.Fatalf("strayProjectManifests: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none -- a deleted project must stay deletable, not wedge on a false positive", got)
	}
}

func TestStrayProjectManifests_FindsSurvivingProjectYaml(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"clusters/beget-prod/projects/gone/project.yaml": "kind: Project\n",
	})

	got, err := strayProjectManifests(root, "gone")
	if err != nil {
		t.Fatalf("strayProjectManifests: %v", err)
	}
	want := "clusters/beget-prod/projects/gone/project.yaml"
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %v, want [%s]", got, want)
	}
}

// TestStrayProjectManifests_FindsOtherClusterPrefix is the case the delete
// itself cannot see: doDeleteProject only ever targets
// clusters/beget-prod/projects/<slug>, so a tree of the same project sitting
// under a different cluster prefix survives the remove untouched. The scan
// must catch it anyway, or the wipe below proceeds while that tree keeps
// deploying with no DB row left to identify it by.
func TestStrayProjectManifests_FindsOtherClusterPrefix(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"clusters/other-cluster/projects/gone/environments/prod/apps/web/app.yaml": "kind: Application\n",
	})

	got, err := strayProjectManifests(root, "gone")
	if err != nil {
		t.Fatalf("strayProjectManifests: %v", err)
	}
	want := "clusters/other-cluster/projects/gone/environments/prod/apps/web/app.yaml"
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %v, want [%s]", got, want)
	}
}

// TestStrayProjectManifests_IgnoresSimilarSlug guards the exact-segment match:
// a project named "foo" must not be confused with a sibling tree "foo-bar",
// or every delete of "foo" would falsely refuse once "foo-bar" exists.
func TestStrayProjectManifests_IgnoresSimilarSlug(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"clusters/beget-prod/projects/foo-bar/project.yaml": "kind: Project\n",
	})

	got, err := strayProjectManifests(root, "foo")
	if err != nil {
		t.Fatalf("strayProjectManifests: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

// TestStrayProjectManifests_IgnoresNestedFileLookalike guards against a file
// merely named project.yaml or app.yaml somewhere deeper in the project's own
// tree that is not the manifest ArgoCD renders (project.yaml only counts
// directly under projects/<slug>/, not further nested).
func TestStrayProjectManifests_IgnoresNestedFileLookalike(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"clusters/beget-prod/projects/gone/environments/prod/apps/web/chart/project.yaml": "nested: true\n",
	})

	got, err := strayProjectManifests(root, "gone")
	if err != nil {
		t.Fatalf("strayProjectManifests: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestVerifyDeleteRemovedProject(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"clusters/beget-prod/projects/gone/project.yaml": "kind: Project\n",
	})

	if err := verifyDeleteRemovedProject(root, "never-deployed", "clusters/x/project.yaml"); err != nil {
		t.Errorf("project with no manifests must be deletable, got %v", err)
	}

	err := verifyDeleteRemovedProject(root, "gone", "clusters/x/project.yaml")
	if err == nil {
		t.Fatal("a still-deployed project must not be reported as deleted")
	}
	if !strings.Contains(err.Error(), "gone/project.yaml") {
		t.Errorf("error must name the surviving manifest so the operator can find it, got %q", err)
	}
}

// newProjectManifestRepo seeds a bare remote carrying project/app manifests at
// the given repo-relative paths and returns a Manager cloned from it.
func newProjectManifestRepo(t *testing.T, paths ...string) *git.Manager {
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
		historyRewriteWriteAndAdd(t, seedDir, wt, p, "kind: Project\n")
	}
	if _, err := wt.Commit("seed project manifests", &gogit.CommitOptions{Author: historyRewriteSig()}); err != nil {
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

// seedDeleteProjectOperation inserts a Processing DeleteProject operation for
// the given project.
func seedDeleteProjectOperation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID, slug string) db.Operation {
	t.Helper()

	var actorID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM users ORDER BY created_at DESC LIMIT 1`,
	).Scan(&actorID); err != nil {
		t.Fatalf("pick actor: %v", err)
	}

	opID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO operations (id, actor_id, project_id, action, resource_kind, resource_name, status)
		 VALUES ($1, $2, $3, 'DeleteProject', 'Project', $4, 'Processing')`,
		opID, actorID, projectID, slug)

	return db.Operation{ID: opID, ProjectID: projectID}
}

// TestDoDeleteProject_SurvivingManifestFailsOperation is the regression for
// the same class of defect verifyDeleteRemovedApp fixed for DeleteApp: a
// DeleteProject whose computed path holds nothing pushes nothing, and without
// this gate the operation would reach Committed with an empty git_commit and
// then wipeProjectRows would erase the project's DB rows while its tree kept
// deploying under a stale cluster prefix. doDeleteProject must return an error
// instead, so poll marks the operation Failed, the DB rows survive, and the
// console shows the user a real fault instead of a silent orphan.
func TestDoDeleteProject_SurvivingManifestFailsOperation(t *testing.T) {
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

	slug := "gone"
	projectID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO users (id, username, email, password_hash, display_name)
		 VALUES ($1, $2, $3, 'x', 'Test')`,
		uuid.New(), "u-"+projectID.String(), projectID.String()+"@test.local")
	exec(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, slug)
	op := seedDeleteProjectOperation(t, ctx, pool, projectID, slug)

	strayPath := "clusters/other-cluster/projects/" + slug + "/project.yaml"
	mgr := newProjectManifestRepo(t, strayPath)

	w := newTestDBWatcher(pool)
	w.cfg = &config.Config{DefaultRepoURL: mgr.RepoURL()}
	w.managers = map[string]*git.Manager{mgr.RepoURL(): mgr}

	err = w.doDeleteProject(ctx, op)
	if err == nil {
		t.Fatal("doDeleteProject reported success while the project's manifest is still in git")
	}
	if !strings.Contains(err.Error(), "other-cluster") {
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

	var projectCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM projects WHERE id = $1`, projectID).Scan(&projectCount); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if projectCount != 1 {
		t.Errorf("project row was wiped despite the surviving git tree: got %d rows, want 1", projectCount)
	}
}

// TestDoDeleteProject_NeverDeployedProjectStaysDeletable is the other half: a
// project that was created but never rendered into git has no manifests
// anywhere, so the delete is an honest no-op and must still complete and wipe
// the DB rows. Failing it would leave imported-but-never-built projects
// permanently undeletable.
func TestDoDeleteProject_NeverDeployedProjectStaysDeletable(t *testing.T) {
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

	slug := "never-shipped"
	projectID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO users (id, username, email, password_hash, display_name)
		 VALUES ($1, $2, $3, 'x', 'Test')`,
		uuid.New(), "u-"+projectID.String(), projectID.String()+"@test.local")
	exec(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, slug)
	op := seedDeleteProjectOperation(t, ctx, pool, projectID, slug)

	mgr := newProjectManifestRepo(t, "clusters/beget-prod/projects/other/project.yaml")

	w := newTestDBWatcher(pool)
	w.cfg = &config.Config{DefaultRepoURL: mgr.RepoURL()}
	w.managers = map[string]*git.Manager{mgr.RepoURL(): mgr}

	if err := w.doDeleteProject(ctx, op); err != nil {
		t.Fatalf("doDeleteProject on a never-deployed project: %v", err)
	}

	var projectCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM projects WHERE id = $1`, projectID).Scan(&projectCount); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if projectCount != 0 {
		t.Errorf("project row survived an honest no-op delete: got %d, want 0", projectCount)
	}
}
