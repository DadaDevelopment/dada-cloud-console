package api

import (
	"context"
	"errors"
	"math/rand"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestResolveGitRepoDistinguishesNoRepoFromNoInstallation guards the bug
// where an app with a template-deployed repo (installation_id IS NULL, the
// normal state for a no-OAuth anonymous clone) was reported by autofix and
// diagnose as having no connected repo at all. Before the LEFT JOIN fix,
// both "no git_repos row" and "row exists but installation_id IS NULL"
// collapsed onto pgx.ErrNoRows via the inner JOIN, so this test would fail
// on the second and third cases: it wants three distinguishable outcomes,
// not two.
func TestResolveGitRepoDistinguishesNoRepoFromNoInstallation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping resolveGitRepo DB integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	h := &Handler{pool: pool}

	suffix := uuid.NewString()[:8]
	var projectID, envID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"resolve-git-repo-test-"+suffix,
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

	t.Run("no git_repos row at all", func(t *testing.T) {
		_, _, _, err := h.resolveGitRepo(ctx, projectID, envID, "app-missing-"+suffix)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("resolveGitRepo() error = %v, want pgx.ErrNoRows", err)
		}
		if errors.Is(err, errRepoWithoutInstallation) {
			t.Fatal("a missing git_repos row must not be reported as errRepoWithoutInstallation")
		}
	})

	t.Run("repo connected without installation (template deploy)", func(t *testing.T) {
		appName := "app-no-install-" + suffix
		if _, err := pool.Exec(ctx,
			`INSERT INTO git_repos (project_id, environment_id, app_name, installation_id, provider, repo_full_name, clone_url)
			 VALUES ($1, $2, $3, NULL, 'github', 'DadaDevelopment/dada-nextjs-starter', 'https://github.com/DadaDevelopment/dada-nextjs-starter.git')`,
			projectID, envID, appName,
		); err != nil {
			t.Fatalf("seed git_repos without installation: %v", err)
		}

		_, instID, _, err := h.resolveGitRepo(ctx, projectID, envID, appName)
		if !errors.Is(err, errRepoWithoutInstallation) {
			t.Fatalf("resolveGitRepo() error = %v, want errRepoWithoutInstallation", err)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			t.Fatal("a repo without installation must not be reported as pgx.ErrNoRows (that means the app has no repo at all)")
		}
		if instID != 0 {
			t.Fatalf("installationID = %d, want 0 when there is no installation", instID)
		}
	})

	t.Run("repo with a live installation", func(t *testing.T) {
		appName := "app-with-install-" + suffix
		var installationRowID uuid.UUID
		numericInstallationID := int64(100000000 + rand.Intn(800000000))
		if err := pool.QueryRow(ctx,
			`INSERT INTO git_app_installations (project_id, org_id, provider, installation_id, account_login, account_type)
			 VALUES ($1, $2, 'github', $3, 'dada-test-org', 'Organization') RETURNING id`,
			projectID, "dada", numericInstallationID,
		).Scan(&installationRowID); err != nil {
			t.Fatalf("seed git_app_installations: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM git_app_installations WHERE id = $1`, installationRowID)
		})
		if _, err := pool.Exec(ctx,
			`INSERT INTO git_repos (project_id, environment_id, app_name, installation_id, provider, repo_full_name, clone_url)
			 VALUES ($1, $2, $3, $4, 'github', 'DadaDevelopment/dada-nextjs-starter', 'https://github.com/DadaDevelopment/dada-nextjs-starter.git')`,
			projectID, envID, appName, installationRowID,
		); err != nil {
			t.Fatalf("seed git_repos with installation: %v", err)
		}

		repo, instID, gitRepoID, err := h.resolveGitRepo(ctx, projectID, envID, appName)
		if err != nil {
			t.Fatalf("resolveGitRepo() error = %v, want nil", err)
		}
		if repo != "DadaDevelopment/dada-nextjs-starter" {
			t.Fatalf("repoFullName = %q, want DadaDevelopment/dada-nextjs-starter", repo)
		}
		if instID != numericInstallationID {
			t.Fatalf("installationID = %d, want %d", instID, numericInstallationID)
		}
		if gitRepoID == uuid.Nil {
			t.Fatal("gitRepoID must not be the zero UUID")
		}
	})
}
