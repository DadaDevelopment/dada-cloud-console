package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedNoDockerfileBuild writes the state a user is left in when the detector
// could not name their archive: an archive repo with no framework and a failed
// build whose reason is no_dockerfile.
func seedNoDockerfileBuild(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName string, finishedAgo time.Duration) (buildID, gitRepoID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx,
		`INSERT INTO git_repos (project_id, environment_id, app_name, provider, repo_full_name, clone_url, production_branch)
		 VALUES ($1, $2, $3, 'archive', $4, $5, 'upload') RETURNING id`,
		projectID, envID, appName, "upload/"+appName, "s3://bucket/source-uploads/"+appName+".tar.gz",
	).Scan(&gitRepoID); err != nil {
		t.Fatalf("seed git_repos: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO builds (git_repo_id, environment_id, app_name, commit_sha, branch, triggered_by, trigger, status,
		                     fail_reason, attempt, created_at, finished_at)
		 VALUES ($1, $2, $3, 'upload', 'upload', $4, 'manual', 'failed', 'no_dockerfile', 1,
		         NOW() - make_interval(secs => $5), NOW() - make_interval(secs => $5)) RETURNING id`,
		gitRepoID, envID, appName, systemDeployActorID, finishedAgo.Seconds(),
	).Scan(&buildID); err != nil {
		t.Fatalf("seed build: %v", err)
	}
	return buildID, gitRepoID
}

func containsCandidate(list []redetectCandidate, buildID uuid.UUID) bool {
	for _, c := range list {
		if c.BuildID == buildID {
			return true
		}
	}
	return false
}

// TestRedetectCandidatesPicksUpStaleNoDockerfileBuild is the "tree" case of
// 2026-08-13: the detector learned the archive's shape hours after the build
// died, and nothing re-asked the question. The sweeper's job is to notice.
func TestRedetectCandidatesPicksUpStaleNoDockerfileBuild(t *testing.T) {
	pool := testSourceArchivePool(t)
	ctx := context.Background()
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "dada")
	buildID, _ := seedNoDockerfileBuild(t, pool, projectID, envID, appName, time.Hour)

	list, err := listRedetectCandidates(ctx, pool)
	if err != nil {
		t.Fatalf("listRedetectCandidates: %v", err)
	}
	if !containsCandidate(list, buildID) {
		t.Fatalf("build %s missing from candidates -- a user blocked on a fixed detector stays blocked", buildID)
	}
}

// TestRedetectCandidatesSkipFreshAndSupersededAndDecided pins the three ways a
// build must be left alone: the user may still be mid-upload, may have already
// re-uploaded, or may have a framework recorded already.
func TestRedetectCandidatesSkipFreshAndSupersededAndDecided(t *testing.T) {
	pool := testSourceArchivePool(t)
	ctx := context.Background()

	fresh, _ := func() (uuid.UUID, uuid.UUID) {
		p, e, a := seedSourceArchiveProject(t, pool, "dada")
		return seedNoDockerfileBuild(t, pool, p, e, a, time.Minute)
	}()

	supersededProject, supersededEnv, supersededApp := seedSourceArchiveProject(t, pool, "dada")
	superseded, supersededRepo := seedNoDockerfileBuild(t, pool, supersededProject, supersededEnv, supersededApp, time.Hour)
	if _, err := pool.Exec(ctx,
		`INSERT INTO builds (git_repo_id, environment_id, app_name, commit_sha, branch, triggered_by, trigger, status)
		 VALUES ($1, $2, $3, 'upload-newer', 'upload', $4, 'manual', 'queued')`,
		supersededRepo, supersededEnv, supersededApp, systemDeployActorID,
	); err != nil {
		t.Fatalf("seed newer build: %v", err)
	}

	decidedProject, decidedEnv, decidedApp := seedSourceArchiveProject(t, pool, "dada")
	decided, decidedRepo := seedNoDockerfileBuild(t, pool, decidedProject, decidedEnv, decidedApp, time.Hour)
	if _, err := pool.Exec(ctx, `UPDATE git_repos SET framework_override = 'python' WHERE id = $1`, decidedRepo); err != nil {
		t.Fatalf("set framework_override: %v", err)
	}

	list, err := listRedetectCandidates(ctx, pool)
	if err != nil {
		t.Fatalf("listRedetectCandidates: %v", err)
	}
	for name, id := range map[string]uuid.UUID{
		"fresh failure":      fresh,
		"superseded failure": superseded,
		"already decided":    decided,
	} {
		if containsCandidate(list, id) {
			t.Errorf("%s (%s) must not be re-queued automatically", name, id)
		}
	}
}

// TestRequeueRedetectedBuildWritesBuildAndAudit pins that a build the user
// never asked for cannot appear without the audit row explaining it, and that
// the attempt counter carries over so the ceiling actually bounds the loop.
func TestRequeueRedetectedBuildWritesBuildAndAudit(t *testing.T) {
	pool := testSourceArchivePool(t)
	ctx := context.Background()
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "dada")
	buildID, gitRepoID := seedNoDockerfileBuild(t, pool, projectID, envID, appName, time.Hour)

	h := &Handler{pool: pool}
	c := redetectCandidate{
		BuildID: buildID, GitRepoID: gitRepoID, ProjectID: projectID, EnvironmentID: envID,
		AppName: appName, Branch: "upload", CloneURL: "s3://bucket/source-uploads/" + appName + ".tar.gz",
	}
	if err := h.requeueRedetectedBuild(ctx, c, "python"); err != nil {
		t.Fatalf("requeueRedetectedBuild: %v", err)
	}

	var status string
	var attempt int
	if err := pool.QueryRow(ctx,
		`SELECT status, attempt FROM builds WHERE git_repo_id = $1 AND id <> $2`, gitRepoID, buildID,
	).Scan(&status, &attempt); err != nil {
		t.Fatalf("read re-queued build: %v", err)
	}
	if status != "queued" || attempt != 2 {
		t.Fatalf("re-queued build = (%s, attempt %d), want (queued, attempt 2)", status, attempt)
	}

	var framework string
	if err := pool.QueryRow(ctx,
		`SELECT metadata->>'redetected_framework' FROM audit_events
		  WHERE action = 'BuildAutoRetried' AND metadata->>'previous_build_id' = $1::text`, buildID,
	).Scan(&framework); err != nil {
		t.Fatalf("read audit row: %v -- an automatic build without a trace reads as a build out of nowhere", err)
	}
	if framework != "python" {
		t.Fatalf("audit redetected_framework = %q, want python", framework)
	}
}
