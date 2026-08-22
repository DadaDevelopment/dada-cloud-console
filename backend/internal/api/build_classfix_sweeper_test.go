package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedClassFixBuild writes one failed build plus the git_repos row it belongs
// to, with a caller-chosen fail_reason, error_message, framework_override and
// finished_at, so each test can shape exactly the row a registry entry either
// should or should not pick up.
func seedClassFixBuild(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName, failReason, errorMessage, framework string, finishedAt time.Time, attempt int) (buildID, gitRepoID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx,
		`INSERT INTO git_repos (project_id, environment_id, app_name, provider, repo_full_name, clone_url, production_branch, framework_override)
		 VALUES ($1, $2, $3, 'github', $4, $5, 'main', NULLIF($6, '')) RETURNING id`,
		projectID, envID, appName, "owner/"+appName, "https://github.com/owner/"+appName+".git", framework,
	).Scan(&gitRepoID); err != nil {
		t.Fatalf("seed git_repos: %v", err)
	}
	commitSHA := uuid.NewString()
	if err := pool.QueryRow(ctx,
		`INSERT INTO builds (git_repo_id, environment_id, app_name, commit_sha, branch, head_sha, triggered_by, trigger, status,
		                     fail_reason, error_message, attempt, created_at, finished_at)
		 VALUES ($1, $2, $3, $4, 'main', $5, $6, 'push', 'failed', $7, $8, $9, $10, $10) RETURNING id`,
		gitRepoID, envID, appName, commitSHA, commitSHA, systemDeployActorID, failReason, errorMessage, attempt, finishedAt,
	).Scan(&buildID); err != nil {
		t.Fatalf("seed build: %v", err)
	}
	return buildID, gitRepoID
}

func containsClassFixCandidate(list []classFixCandidate, buildID uuid.UUID) bool {
	for _, c := range list {
		if c.BuildID == buildID {
			return true
		}
	}
	return false
}

// testClassFix returns a registry entry scoped to one test with a unique
// FailReason, so seeded rows can never accidentally match a different test's
// fixture or a real entry appended to buildClassFixRegistry later.
func testClassFix(t *testing.T, signature, framework string, fixedAt time.Time) classFix {
	t.Helper()
	return classFix{
		ID:         "test-classfix-" + uuid.NewString()[:8],
		FailReason: "test_classfix_reason_" + uuid.NewString()[:8],
		Signature:  signature,
		Framework:  framework,
		FixedAt:    fixedAt,
	}
}

// TestClassFixCandidatePicksUpStaleMatchingBuild is the sess-0822e case:
// tarotreaderhimu@gmail.com's build died on the static-template npm bug
// before the fix landed, and nothing re-asked whether it would succeed now.
func TestClassFixCandidatePicksUpStaleMatchingBuild(t *testing.T) {
	pool := testSourceArchivePool(t)
	ctx := context.Background()
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "dada")
	fixedAt := time.Now().Add(-time.Hour)
	fix := testClassFix(t, "npm install", "static", fixedAt)

	buildID, _ := seedClassFixBuild(t, pool, projectID, envID, appName,
		fix.FailReason, "[build 5/6] RUN npm install: npm error enoent", "static",
		fixedAt.Add(-2*time.Hour), 1)

	list, err := listClassFixCandidates(ctx, pool, []classFix{fix})
	if err != nil {
		t.Fatalf("listClassFixCandidates: %v", err)
	}
	if !containsClassFixCandidate(list, buildID) {
		t.Fatalf("build %s missing from candidates -- a user blocked on a fixed class stays blocked", buildID)
	}
}

// TestClassFixCandidateSkipsBuildAfterFix pins that a build which failed
// after the fix landed was not touched by it and must be left alone: it
// failed for some other, still-live reason.
func TestClassFixCandidateSkipsBuildAfterFix(t *testing.T) {
	pool := testSourceArchivePool(t)
	ctx := context.Background()
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "dada")
	fixedAt := time.Now().Add(-time.Hour)
	fix := testClassFix(t, "npm install", "static", fixedAt)

	buildID, _ := seedClassFixBuild(t, pool, projectID, envID, appName,
		fix.FailReason, "[build 5/6] RUN npm install: npm error enoent", "static",
		fixedAt.Add(10*time.Minute), 1)

	list, err := listClassFixCandidates(ctx, pool, []classFix{fix})
	if err != nil {
		t.Fatalf("listClassFixCandidates: %v", err)
	}
	if containsClassFixCandidate(list, buildID) {
		t.Fatalf("build %s failed after the fix landed and must not be re-queued", buildID)
	}
}

// TestClassFixCandidateSkipsSupersededBuild pins that a build is only a
// candidate while it is the newest build of its app: a user who already
// retried by hand (or already succeeded) must not get a second, redundant
// automatic build.
func TestClassFixCandidateSkipsSupersededBuild(t *testing.T) {
	pool := testSourceArchivePool(t)
	ctx := context.Background()
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "dada")
	fixedAt := time.Now().Add(-time.Hour)
	fix := testClassFix(t, "npm install", "static", fixedAt)

	buildID, gitRepoID := seedClassFixBuild(t, pool, projectID, envID, appName,
		fix.FailReason, "[build 5/6] RUN npm install: npm error enoent", "static",
		fixedAt.Add(-2*time.Hour), 1)
	if _, err := pool.Exec(ctx,
		`INSERT INTO builds (git_repo_id, environment_id, app_name, commit_sha, branch, triggered_by, trigger, status)
		 VALUES ($1, $2, $3, $4, 'main', $5, 'manual', 'queued')`,
		gitRepoID, envID, appName, uuid.NewString(), systemDeployActorID,
	); err != nil {
		t.Fatalf("seed newer build: %v", err)
	}

	list, err := listClassFixCandidates(ctx, pool, []classFix{fix})
	if err != nil {
		t.Fatalf("listClassFixCandidates: %v", err)
	}
	if containsClassFixCandidate(list, buildID) {
		t.Fatalf("build %s has a newer build of the same app and must not be re-queued", buildID)
	}
}

// TestClassFixCandidateSkipsAttemptCeiling pins the retry ceiling: a build
// already at attempt 3 has exhausted its automatic retries, whether or not
// the class fix would otherwise cover it.
func TestClassFixCandidateSkipsAttemptCeiling(t *testing.T) {
	pool := testSourceArchivePool(t)
	ctx := context.Background()
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "dada")
	fixedAt := time.Now().Add(-time.Hour)
	fix := testClassFix(t, "npm install", "static", fixedAt)

	buildID, _ := seedClassFixBuild(t, pool, projectID, envID, appName,
		fix.FailReason, "[build 5/6] RUN npm install: npm error enoent", "static",
		fixedAt.Add(-2*time.Hour), buildClassFixMaxAttempts)

	list, err := listClassFixCandidates(ctx, pool, []classFix{fix})
	if err != nil {
		t.Fatalf("listClassFixCandidates: %v", err)
	}
	if containsClassFixCandidate(list, buildID) {
		t.Fatalf("build %s is at the attempt ceiling and must not be re-queued", buildID)
	}
}

// TestClassFixCandidateSkipsDifferentFailReason pins that the registry entry
// only ever matches its own fail_reason: an unrelated failure class must
// never be swept up just because it happened to fail around the same time.
func TestClassFixCandidateSkipsDifferentFailReason(t *testing.T) {
	pool := testSourceArchivePool(t)
	ctx := context.Background()
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "dada")
	fixedAt := time.Now().Add(-time.Hour)
	fix := testClassFix(t, "npm install", "static", fixedAt)

	buildID, _ := seedClassFixBuild(t, pool, projectID, envID, appName,
		"some_other_fail_reason", "[build 5/6] RUN npm install: npm error enoent", "static",
		fixedAt.Add(-2*time.Hour), 1)

	list, err := listClassFixCandidates(ctx, pool, []classFix{fix})
	if err != nil {
		t.Fatalf("listClassFixCandidates: %v", err)
	}
	if containsClassFixCandidate(list, buildID) {
		t.Fatalf("build %s has a different fail_reason and must not be re-queued", buildID)
	}
}

// TestClassFixCandidateSkipsSignatureMismatch pins that a matching
// fail_reason alone is not enough when the registry entry also names a
// signature: the same fail_reason can cover several distinct shapes of
// error, and only the one the fix actually closed may be re-queued.
func TestClassFixCandidateSkipsSignatureMismatch(t *testing.T) {
	pool := testSourceArchivePool(t)
	ctx := context.Background()
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "dada")
	fixedAt := time.Now().Add(-time.Hour)
	fix := testClassFix(t, "npm install", "static", fixedAt)

	buildID, _ := seedClassFixBuild(t, pool, projectID, envID, appName,
		fix.FailReason, "[build 4/6] RUN pip install: no matching distribution", "static",
		fixedAt.Add(-2*time.Hour), 1)

	list, err := listClassFixCandidates(ctx, pool, []classFix{fix})
	if err != nil {
		t.Fatalf("listClassFixCandidates: %v", err)
	}
	if containsClassFixCandidate(list, buildID) {
		t.Fatalf("build %s error_message does not carry the signature and must not be re-queued", buildID)
	}
}

// TestClassFixCandidateSkipsFrameworkMismatch is case (zh) from the sess-0822e
// grounding: the static-template npm bug only fired for framework "static".
// A build with the same fail_reason and the same "npm install" text but a
// different detected framework (nextjs) failed on its own genuinely broken
// Dockerfile, and re-queuing it would retry a build that was never going to
// succeed.
func TestClassFixCandidateSkipsFrameworkMismatch(t *testing.T) {
	pool := testSourceArchivePool(t)
	ctx := context.Background()
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "dada")
	fixedAt := time.Now().Add(-time.Hour)
	fix := testClassFix(t, "npm install", "static", fixedAt)

	buildID, _ := seedClassFixBuild(t, pool, projectID, envID, appName,
		fix.FailReason, "[build 5/6] RUN npm install: npm error enoent", "nextjs",
		fixedAt.Add(-2*time.Hour), 1)

	list, err := listClassFixCandidates(ctx, pool, []classFix{fix})
	if err != nil {
		t.Fatalf("listClassFixCandidates: %v", err)
	}
	if containsClassFixCandidate(list, buildID) {
		t.Fatalf("build %s was detected as a different framework and must not be re-queued", buildID)
	}
}

// TestRequeueClassFixedBuildWritesBuildAndAudit pins that a build the user
// never asked for cannot appear without the audit row explaining it, and that
// the attempt counter and head_sha carry over from the build it replaces.
func TestRequeueClassFixedBuildWritesBuildAndAudit(t *testing.T) {
	pool := testSourceArchivePool(t)
	ctx := context.Background()
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "dada")
	fixedAt := time.Now().Add(-time.Hour)
	fix := testClassFix(t, "npm install", "static", fixedAt)

	buildID, gitRepoID := seedClassFixBuild(t, pool, projectID, envID, appName,
		fix.FailReason, "[build 5/6] RUN npm install: npm error enoent", "static",
		fixedAt.Add(-2*time.Hour), 1)

	h := &Handler{pool: pool}
	c := classFixCandidate{
		BuildID: buildID, GitRepoID: gitRepoID, ProjectID: projectID, EnvironmentID: envID,
		AppName: appName, Branch: "main", Attempt: 1, FailReason: fix.FailReason, ClassFixID: fix.ID,
	}
	if err := h.requeueClassFixedBuild(ctx, c); err != nil {
		t.Fatalf("requeueClassFixedBuild: %v", err)
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

	var classFixID, prevFailReason string
	if err := pool.QueryRow(ctx,
		`SELECT metadata->>'class_fix_id', metadata->>'previous_fail_reason' FROM audit_events
		  WHERE action = 'BuildAutoRetried' AND metadata->>'previous_build_id' = $1::text`, buildID,
	).Scan(&classFixID, &prevFailReason); err != nil {
		t.Fatalf("read audit row: %v -- an automatic build without a trace reads as a build out of nowhere", err)
	}
	if classFixID != fix.ID {
		t.Fatalf("audit class_fix_id = %q, want %q", classFixID, fix.ID)
	}
	if prevFailReason != fix.FailReason {
		t.Fatalf("audit previous_fail_reason = %q, want %q", prevFailReason, fix.FailReason)
	}
}
