package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestGitReposReplicasColumnDefaultsToOne pins the column default that decided
// the shape of every git- and archive-born app on the platform.
//
// 023_git_repos_app_spec.sql created git_repos.replicas with DEFAULT 2. Nothing
// in the product ever asked for two: the create form sends 1, CreateApp defaults
// to 1, ConnectGitRepo was fixed to send 1 in 52f00c47. But an INSERT that omits
// the column still gets 2 from the database, and build-agent's first-build
// materialization reads that row straight into the CreateApp payload -- which is
// how the console itself committed `replicas: 2` to argo-infra for megafactory,
// a2ahub-landing, dada-development-site and the whole upload cohort.
//
// The assertion is on the schema, not on a code path, because the default is
// what every current and future omitting INSERT inherits.
func TestGitReposReplicasColumnDefaultsToOne(t *testing.T) {
	pool := testSourceArchivePool(t)

	var def *string
	if err := pool.QueryRow(context.Background(),
		`SELECT column_default FROM information_schema.columns
		 WHERE table_name = 'git_repos' AND column_name = 'replicas'`,
	).Scan(&def); err != nil {
		t.Fatalf("read column default: %v", err)
	}
	if def == nil {
		t.Fatal("git_repos.replicas has no default; an omitting INSERT would fail on NOT NULL")
	}
	if *def != "1" {
		t.Fatalf("git_repos.replicas default = %q, want \"1\" -- an app nobody asked to scale is born with %s pods", *def, *def)
	}
}

// TestArchiveUploadRepoIsBornWithOneReplica walks the actual bug path: the
// upload flow's INSERT names every column it has an opinion about and leaves
// replicas to the database, so whatever the schema says is what the first build
// deploys.
func TestArchiveUploadRepoIsBornWithOneReplica(t *testing.T) {
	pool := testSourceArchivePool(t)
	ctx := context.Background()
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "dada")

	var gitRepoID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO git_repos
		   (project_id, environment_id, app_name, provider, repo_full_name, clone_url,
		    production_branch, port, worker)
		 VALUES ($1, $2, $3, 'archive', $4, $5, 'upload', 8080, false)
		 RETURNING id`,
		projectID, envID, appName, "upload/"+appName, "s3://bucket/source-uploads/"+appName+".tar.gz",
	).Scan(&gitRepoID); err != nil {
		t.Fatalf("seed archive git_repos: %v", err)
	}

	var replicas int
	if err := pool.QueryRow(ctx, `SELECT replicas FROM git_repos WHERE id = $1`, gitRepoID).Scan(&replicas); err != nil {
		t.Fatalf("read replicas: %v", err)
	}
	if replicas != 1 {
		t.Fatalf("archive-uploaded repo has replicas = %d, want 1 -- build-agent hands this straight to CreateApp", replicas)
	}
}
