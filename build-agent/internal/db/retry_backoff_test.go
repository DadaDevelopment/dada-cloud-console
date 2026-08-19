package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedInFlightBuild seeds a build the runner is currently executing, which is
// the only shape RequeueForRetry acts on.
func seedInFlightBuild(t *testing.T, pool *pgxpool.Pool, gitRepoID, envID uuid.UUID, appName string, attempt int) uuid.UUID {
	t.Helper()
	buildID := uuid.New()
	exec(t, pool,
		`INSERT INTO builds (id, git_repo_id, environment_id, app_name, commit_sha, branch, trigger, status, attempt, started_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, 'main', 'push', 'building', $6, NOW(), NOW())`,
		buildID, gitRepoID, envID, appName, "sha-"+uuid.New().String()[:8], attempt)
	t.Cleanup(func() { exec(t, pool, `DELETE FROM builds WHERE id = $1`, buildID) })
	return buildID
}

// TestRequeueForRetry_HoldsTheRetryBackFromTheSameOutage is the fanvk case one
// layer down: a 503 from the Jenkins ingress re-queued the build, the very
// next drain tick claimed it into the same dead ingress, and all three
// attempts were spent inside a few seconds of an outage that cleared a minute
// later.
func TestRequeueForRetry_HoldsTheRetryBackFromTheSameOutage(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	ownerID := seedUser(t, pool)
	gitRepoID := seedGitRepoOwned(t, pool, projectID, envID, "app-backoff", ownerID)
	buildID := seedInFlightBuild(t, pool, gitRepoID, envID, "app-backoff", 0)

	retried, retryAt, err := RequeueForRetry(ctx, pool, buildID, "transient jenkins error", 3)
	if err != nil || !retried {
		t.Fatalf("RequeueForRetry = (%v,%v), want (true,nil)", retried, err)
	}
	if d := time.Until(retryAt); d < 20*time.Second || d > retryBackoffBaseSeconds*time.Second+5*time.Second {
		t.Fatalf("retry_after is %s away, want about %ds", d, retryBackoffBaseSeconds)
	}

	claimed, err := ClaimQueued(ctx, pool)
	if err != nil {
		t.Fatalf("ClaimQueued: %v", err)
	}
	if claimed != nil && claimed.ID == buildID {
		t.Fatal("a held build was claimed immediately: the retry lands in the same outage it just failed in")
	}

	exec(t, pool, `UPDATE builds SET retry_after = NOW() - interval '1 second' WHERE id = $1`, buildID)
	claimed, err = ClaimQueued(ctx, pool)
	if err != nil {
		t.Fatalf("ClaimQueued after the hold expired: %v", err)
	}
	if claimed == nil || claimed.ID != buildID {
		t.Fatalf("claimed = %v, want the build to run once its hold expired", claimed)
	}
}

// TestRequeueForRetry_BackoffGrowsWithAttempts pins the doubling: a second
// transient failure waits longer than the first, so a build does not spend its
// whole budget inside one outage.
func TestRequeueForRetry_BackoffGrowsWithAttempts(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	ownerID := seedUser(t, pool)
	gitRepoID := seedGitRepoOwned(t, pool, projectID, envID, "app-backoff2", ownerID)

	first := seedInFlightBuild(t, pool, gitRepoID, envID, "app-backoff2", 0)
	second := seedInFlightBuild(t, pool, gitRepoID, envID, "app-backoff2", 1)

	_, firstAt, err := RequeueForRetry(ctx, pool, first, "transient", 3)
	if err != nil {
		t.Fatalf("requeue first: %v", err)
	}
	_, secondAt, err := RequeueForRetry(ctx, pool, second, "transient", 3)
	if err != nil {
		t.Fatalf("requeue second: %v", err)
	}
	if !secondAt.After(firstAt.Add(20 * time.Second)) {
		t.Fatalf("attempt 1 waits until %s, attempt 2 until %s: the backoff does not grow", firstAt, secondAt)
	}
}

// TestRequeueForRetry_ExhaustedAttemptsReportNoRetry keeps the caller's
// fallback intact: no rows updated must read as "not retried", not as an error.
func TestRequeueForRetry_ExhaustedAttemptsReportNoRetry(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	ownerID := seedUser(t, pool)
	gitRepoID := seedGitRepoOwned(t, pool, projectID, envID, "app-backoff3", ownerID)
	buildID := seedInFlightBuild(t, pool, gitRepoID, envID, "app-backoff3", 3)

	retried, _, err := RequeueForRetry(ctx, pool, buildID, "transient", 3)
	if err != nil {
		t.Fatalf("RequeueForRetry with a spent budget returned an error: %v", err)
	}
	if retried {
		t.Fatal("a build past its attempt budget was re-queued")
	}
}

// TestRetryPlatformFailedBuilds_ClearsTheHold proves the recovery sweeper is
// not throttled by a hold left on the row by an earlier transient retry: the
// platform is healthy again by the time it runs.
func TestRetryPlatformFailedBuilds_ClearsTheHold(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	ownerID := seedUser(t, pool)
	gitRepoID := seedGitRepoOwned(t, pool, projectID, envID, "app-hold", ownerID)
	buildID := seedPlatformFailedBuild(t, pool, gitRepoID, envID, "app-hold", "sha-hold-"+uuid.New().String()[:8], time.Hour, 0)
	exec(t, pool, `UPDATE builds SET retry_after = NOW() + interval '1 hour' WHERE id = $1`, buildID)

	if _, err := RetryPlatformFailedBuilds(ctx, pool, 10*time.Minute, 24*time.Hour, 3); err != nil {
		t.Fatalf("RetryPlatformFailedBuilds: %v", err)
	}
	var hold *time.Time
	if err := pool.QueryRow(ctx, `SELECT retry_after FROM builds WHERE id = $1`, buildID).Scan(&hold); err != nil {
		t.Fatalf("read retry_after: %v", err)
	}
	if hold != nil {
		t.Fatalf("retry_after = %v, want NULL: the recovery pass must not leave the build parked", *hold)
	}
}
