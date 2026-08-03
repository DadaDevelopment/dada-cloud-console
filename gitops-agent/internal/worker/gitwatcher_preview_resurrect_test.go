package worker

import (
	"context"
	"os"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPreviewEnvNameRe pins which environment names the watcher refuses to
// auto-create. The pattern must match exactly what build-agent's
// EnsurePreviewEnv mints ("pr-<n>-<app>") and nothing a human would name a
// long-lived environment.
func TestPreviewEnvNameRe(t *testing.T) {
	cases := []struct {
		name    string
		preview bool
	}{
		{"pr-6-fonbet-value", true},
		{"pr-7-fonbet-value", true},
		{"pr-1-tvk-assistantbot", true},
		{"pr-123-web", true},
		{"prod", false},
		{"staging", false},
		{"preview", false},
		{"pr-web", false},
		{"findata", false},
		{"my-pr-2-web", false},
	}
	for _, tc := range cases {
		if got := previewEnvNameRe.MatchString(tc.name); got != tc.preview {
			t.Errorf("previewEnvNameRe.MatchString(%q) = %v, want %v", tc.name, got, tc.preview)
		}
	}
}

// TestProcessCommit_TornDownPreviewNotResurrected is the regression test for the
// 2026-08-03 prod incident. Four PR previews were torn down (their environments
// rows deleted) between 07-30 and 08-01; on 08-03 the watcher replayed the older
// commits that had once added their app files, and resolveOrCreateProjectEnv
// recreated each one as a plain type=prod environment with is_ephemeral false
// and expires_at NULL — immortal to the TTL reaper, and sorting ahead of the
// real "prod" env, which made the project overview report "0 apps" for a project
// whose app was Ready and serving traffic.
//
// Replaying an add of a preview app file must now leave the database untouched.
// Gated on TEST_DATABASE_URL, mirroring TestProcessCommit_DeletedProjectNotRecreated.
func TestProcessCommit_TornDownPreviewNotResurrected(t *testing.T) {
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

	projectID := uuid.New()
	slug := "preview-resurrect-" + projectID.String()[:8]
	exec(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name, default_environment) VALUES ($1, $2, $3, 'prod')`,
		projectID, slug, "Preview resurrect")
	envID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type)
		 VALUES ($1, $2, 'prod', $3, 'prod')`,
		envID, projectID, slug+"-prod")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM environments WHERE project_id = $1`, projectID)
		_, _ = pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, projectID)
	})

	mgr := git.New(git.RepoConfig{
		RepoURL:   "https://example.com/org/argo-infra.git",
		Branch:    "main",
		LocalBase: t.TempDir(),
	})

	base := "clusters/beget-prod/projects/" + slug + "/environments/pr-6-web/apps/web/"
	addCommit := git.Commit{
		SHA:     "1111111111111111111111111111111111111111",
		Message: "[DADA Console] Deploy preview pr-6-web",
		Author:  "gitops-agent",
		Email:   "bot@dada",
		Files:   []string{base + "app.yaml", base + "values.yaml", base + "resources.values.yaml"},
	}

	w := &GitWatcher{pool: pool}
	w.processCommit(ctx, mgr, addCommit)

	var envs int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM environments WHERE project_id = $1 AND name = 'pr-6-web'`,
		projectID).Scan(&envs); err != nil {
		t.Fatalf("count preview envs: %v", err)
	}
	if envs != 0 {
		t.Fatalf("git watcher resurrected a torn-down preview environment: %d rows, want 0", envs)
	}

	var snaps int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM resource_snapshots WHERE project_id = $1`,
		projectID).Scan(&snaps); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	if snaps != 0 {
		t.Fatalf("preview app files leaked %d resource_snapshots rows, want 0", snaps)
	}
}
