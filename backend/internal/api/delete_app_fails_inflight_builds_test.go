package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// seedGitRepoForDelete inserts the one git_repos row an app can have
// (project_id, environment_id, app_name is unique), for seedBuildForDelete to
// hang builds off of.
func seedGitRepoForDelete(t *testing.T, projectID, envID uuid.UUID, appName string) uuid.UUID {
	t.Helper()
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	var gitRepoID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO git_repos (project_id, environment_id, app_name, provider, repo_full_name, clone_url, production_branch)
		 VALUES ($1, $2, $3, 'github', $4, $5, 'main')
		 RETURNING id`,
		projectID, envID, appName, "acme/"+appName, "https://github.com/acme/"+appName+".git",
	).Scan(&gitRepoID); err != nil {
		t.Fatalf("seed git_repos: %v", err)
	}
	return gitRepoID
}

// seedBuildForDelete inserts a builds row owned by (envID, appName) in the
// given status, against gitRepoID (builds.git_repo_id has no default and is
// read by the FK, so a real row is needed even though this test never
// inspects it). commitSHA must be unique per build sharing a git_repo_id
// (builds_git_repo_id_commit_sha_key).
func seedBuildForDelete(t *testing.T, gitRepoID, envID uuid.UUID, appName, commitSHA, status string) uuid.UUID {
	t.Helper()
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	var buildID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO builds (git_repo_id, environment_id, app_name, commit_sha, branch, trigger, status)
		 VALUES ($1, $2, $3, $4, 'main', 'push', $5)
		 RETURNING id`,
		gitRepoID, envID, appName, commitSHA, status,
	).Scan(&buildID); err != nil {
		t.Fatalf("seed build: %v", err)
	}
	return buildID
}

func readBuildStatusAndReason(t *testing.T, envID uuid.UUID, buildID uuid.UUID) (status string, failReason *string) {
	t.Helper()
	pool := testAdvisoryPool(t)
	if err := pool.QueryRow(context.Background(),
		`SELECT status, fail_reason FROM builds WHERE id = $1`, buildID,
	).Scan(&status, &failReason); err != nil {
		t.Fatalf("read back build: %v", err)
	}
	return
}

// TestDeleteAppFailsInFlightBuild pins the fix for the instatic shape: a live
// user (kkartov@yandex.ru, 2026-08-18) ran Connect->Build->Delete seven times
// in ~7 hours, and three of his builds died with "load repo
// 00000000-0000-0000-0000-000000000000: no rows in result set" -- the
// zero-UUID artifact of build-agent scanning a NULLed git_repo_id (migration
// 116's FK is ON DELETE SET NULL, and build-agent's Build.GitRepoID is a
// non-pointer uuid.UUID) into a repo lookup that can never succeed.
//
// DeleteApp must close out any build still in flight for the app BEFORE that
// race can start, with a human fail_reason ("app_deleted"), not let it limp
// forward to hit the zero-UUID lookup.
func TestDeleteAppFailsInFlightBuild(t *testing.T) {
	pool := testAdvisoryPool(t)

	projectID, envID := seedReattachProjectEnv(t, pool)
	userID := seedUser(t, pool)
	claims := godClaims(userID)
	appName := "instatic-" + uuid.NewString()[:8]
	seedReattachApp(t, pool, projectID, envID, appName, `{"port":8080}`)

	gitRepoID := seedGitRepoForDelete(t, projectID, envID, appName)
	buildingID := seedBuildForDelete(t, gitRepoID, envID, appName, "commit-building", "building")
	pushingID := seedBuildForDelete(t, gitRepoID, envID, appName, "commit-pushing", "pushing")
	queuedID := seedBuildForDelete(t, gitRepoID, envID, appName, "commit-queued", "queued")
	successID := seedBuildForDelete(t, gitRepoID, envID, appName, "commit-success", "success")

	h := &Handler{pool: pool}
	c, rec := newCreateCtx(t, "", ginParams(projectID, envID, appName), claims)
	h.DeleteApp(c)
	if rec.Code != 202 {
		t.Fatalf("DeleteApp status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	for _, tc := range []struct {
		name    string
		buildID uuid.UUID
	}{
		{"building", buildingID},
		{"pushing", pushingID},
		{"queued", queuedID},
	} {
		status, failReason := readBuildStatusAndReason(t, envID, tc.buildID)
		if status != "failed" {
			t.Fatalf("%s build status = %q, want failed (must not be left to race the git_repos delete)", tc.name, status)
		}
		if failReason == nil || *failReason != "app_deleted" {
			t.Fatalf("%s build fail_reason = %v, want app_deleted", tc.name, failReason)
		}
	}

	status, _ := readBuildStatusAndReason(t, envID, successID)
	if status != "success" {
		t.Fatalf("already-terminal build status = %q, want left alone as success", status)
	}
}
