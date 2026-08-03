package api

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testInstallBindPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping installation-bind DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedInstallBindProject creates a project in its own org so installations
// seeded for it cannot be reached from any other test's project.
func seedInstallBindProject(t *testing.T, pool *pgxpool.Pool, org string) uuid.UUID {
	t.Helper()
	suffix := uuid.NewString()[:8]
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $1, $2) RETURNING id`,
		"install-bind-"+suffix, org,
	).Scan(&id); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() {
		dropSeededProject(pool, id)
	})
	return id
}

func seedInstallation(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID, org, login string, numericID int64) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO git_app_installations (project_id, org_id, provider, installation_id, account_login, account_type)
		 VALUES ($1, $2, 'github', $3, $4, 'Organization') RETURNING id`,
		projectID, org, numericID, login,
	).Scan(&id); err != nil {
		t.Fatalf("seed installation: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM git_app_installations WHERE id = $1`, id)
	})
	return id
}

func TestResolveInstallationByOwner(t *testing.T) {
	pool := testInstallBindPool(t)
	h := &Handler{pool: pool}
	ctx := context.Background()

	org := "install-bind-org-" + uuid.NewString()[:8]
	projectID := seedInstallBindProject(t, pool, org)
	instID := seedInstallation(t, pool, projectID, org, "AcmeDev", 5550001)

	t.Run("binds the org installation matching the repo owner", func(t *testing.T) {
		got, ok := h.resolveInstallationByOwner(ctx, projectID, "AcmeDev/private-repo")
		if !ok || got != instID {
			t.Fatalf("got (%v, %v) want (%v, true)", got, ok, instID)
		}
	})

	t.Run("owner match is case-insensitive", func(t *testing.T) {
		got, ok := h.resolveInstallationByOwner(ctx, projectID, "acmedev/private-repo")
		if !ok || got != instID {
			t.Fatalf("got (%v, %v) want (%v, true)", got, ok, instID)
		}
	})

	t.Run("no match for a different owner", func(t *testing.T) {
		if got, ok := h.resolveInstallationByOwner(ctx, projectID, "SomeoneElse/repo"); ok {
			t.Fatalf("got (%v, true) want no match", got)
		}
	})

	t.Run("no match without an owner segment", func(t *testing.T) {
		if got, ok := h.resolveInstallationByOwner(ctx, projectID, "bare-repo-name"); ok {
			t.Fatalf("got (%v, true) want no match", got)
		}
	})

	t.Run("does not cross the org boundary", func(t *testing.T) {
		otherOrg := "install-bind-other-" + uuid.NewString()[:8]
		otherProject := seedInstallBindProject(t, pool, otherOrg)
		if got, ok := h.resolveInstallationByOwner(ctx, otherProject, "AcmeDev/private-repo"); ok {
			t.Fatalf("got (%v, true) want no match across orgs", got)
		}
	})

	t.Run("ambiguous owner resolves to no match", func(t *testing.T) {
		ambigOrg := "install-bind-ambig-" + uuid.NewString()[:8]
		ambigProject := seedInstallBindProject(t, pool, ambigOrg)
		seedInstallation(t, pool, ambigProject, ambigOrg, "DupeDev", 5550002)
		seedInstallation(t, pool, ambigProject, ambigOrg, "DupeDev", 5550003)
		if got, ok := h.resolveInstallationByOwner(ctx, ambigProject, "DupeDev/repo"); ok {
			t.Fatalf("got (%v, true) want no match when two installations claim the owner", got)
		}
	})
}
