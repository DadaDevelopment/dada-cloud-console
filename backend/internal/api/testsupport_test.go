package api

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// dropSeededProject removes a seeded test project and the rows that reference
// it, child-first.
//
// A bare `DELETE FROM projects` cannot succeed while a snapshot, git_repo,
// env_var or environment still points at the project: postgres answers 23503
// and the usual `_, _ = pool.Exec(...)` cleanup swallows it. The leftovers are
// not harmless. One of them -- project volume-export-test-8b671f4e with a
// phase-less App snapshot, seeded 2026-07-31 -- survived in the shared
// cloud-console database and made /admin/overview answer 500 for every caller
// until migration 096 landed.
func dropSeededProject(pool *pgxpool.Pool, projectID uuid.UUID) {
	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM resource_snapshots WHERE project_id = $1`,
		`DELETE FROM git_repos WHERE project_id = $1`,
		`DELETE FROM env_vars WHERE environment_id IN (SELECT id FROM environments WHERE project_id = $1)`,
		`DELETE FROM environments WHERE project_id = $1`,
		`DELETE FROM projects WHERE id = $1`,
	} {
		_, _ = pool.Exec(ctx, stmt, projectID)
	}
}
