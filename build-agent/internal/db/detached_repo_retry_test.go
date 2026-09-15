package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedDetachedBuild seeds a build whose git_repos row is gone (git_repo_id
// NULL), the exact shape migration 155 is written for: DeleteApp races a
// build, gitops-agent's deleteAppGitRepo drops the git_repos row, and FK ON
// DELETE SET NULL (migration 116) leaves builds.git_repo_id NULL on the
// survivor. status selects the zombie shape: 'failed' + platform_error is
// what the self-heal pass used to requeue forever, 'building' is the row the
// runner itself dies on at LoadRepo.
func seedDetachedBuild(t *testing.T, pool *pgxpool.Pool, envID uuid.UUID, appName, status string, attempt int) uuid.UUID {
	t.Helper()
	buildID := uuid.New()
	exec(t, pool,
		`INSERT INTO builds (id, git_repo_id, environment_id, app_name, commit_sha, branch, trigger, status,
		                     fail_reason, error_message, attempt, started_at, created_at)
		 VALUES ($1, NULL, $2, $3, 'sha-detached-' || $4, 'main', 'push', $5,
		         'platform_error', 'load repo: load repo 00000000-0000-0000-0000-000000000000: no rows in result set', $6, NOW(), NOW())`,
		buildID, envID, appName, uuid.NewString()[:8], status, attempt)
	t.Cleanup(func() {
		exec(t, pool, `DELETE FROM builds WHERE id = $1`, buildID)
	})
	return buildID
}

// TestRetryPlatformFailedBuilds_SkipsDetachedRepoZombies is the freitorsk case
// (2026-09-12): a build whose repo was unlinked mid-run kept cycling through
// platform_error retries for hours while its owner watched a spinning build
// and finally deleted the app. The recovery pass must leave rows whose
// git_repo_id is NULL alone: no repo means nothing to retry into.
func TestRetryPlatformFailedBuilds_SkipsDetachedRepoZombies(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	ownerID := seedUser(t, pool)
	seedGitRepoOwned(t, pool, projectID, envID, "app-zombie", ownerID)

	buildID := seedDetachedBuild(t, pool, envID, "app-zombie", "failed", 6)

	ids, err := RetryPlatformFailedBuilds(ctx, pool, 10*time.Minute, 24*time.Hour, PlatformRecoveryMaxAttempts)
	if err != nil {
		t.Fatalf("RetryPlatformFailedBuilds: %v", err)
	}
	if containsID(ids, buildID) {
		t.Fatalf("detached build %s was requeued; zombie builds must stay failed", buildID)
	}

	status, failReason, attempt := buildRetryState(t, pool, buildID)
	if status != "failed" {
		t.Fatalf("detached build status = %q, want failed (untouched)", status)
	}
	if failReason == nil || *failReason != "platform_error" {
		t.Fatalf("detached build fail_reason = %v, want platform_error (untouched)", failReason)
	}
	if attempt != 6 {
		t.Fatalf("detached build attempt = %d, want 6 (untouched)", attempt)
	}
}

// TestRequeueForRetry_RejectsDetachedRepo pins the retry primitive itself: a
// NULL git_repo_id row is unexecutable by construction, so RequeueForRetry
// must refuse it even if a future caller hands one over directly.
func TestRequeueForRetry_RejectsDetachedRepo(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	_, envID := seedProjectEnv(t, pool, "small")

	buildID := seedDetachedBuild(t, pool, envID, "app-zombie-direct", "building", 1)

	requeued, _, err := RequeueForRetry(ctx, pool, buildID, "transient outage", 4)
	if err != nil {
		t.Fatalf("RequeueForRetry: %v", err)
	}
	if requeued {
		t.Fatalf("RequeueForRetry requeued detached build %s; it must refuse NULL-repo rows", buildID)
	}

	status, _, attempt := buildRetryState(t, pool, buildID)
	if status != "building" {
		t.Fatalf("detached build status = %q, want building (untouched)", status)
	}
	if attempt != 1 {
		t.Fatalf("detached build attempt = %d, want 1 (untouched)", attempt)
	}
}
