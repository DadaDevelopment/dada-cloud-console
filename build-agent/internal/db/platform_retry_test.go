package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedPlatformFailedBuild seeds a build that already ended failed with the
// platform's own reason code, aged by finishedAgo, which is the exact row the
// recovery pass exists for.
func seedPlatformFailedBuild(t *testing.T, pool *pgxpool.Pool, gitRepoID, envID uuid.UUID, appName, commitSHA string, finishedAgo time.Duration, attempt int) uuid.UUID {
	t.Helper()
	buildID := uuid.New()
	exec(t, pool,
		`INSERT INTO builds (id, git_repo_id, environment_id, app_name, commit_sha, branch, trigger, status,
		                     fail_reason, error_message, attempt, started_at, finished_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, 'main', 'push', 'failed',
		         'platform_error', 'Maximum checkout retry attempts reached, aborting', $6, $7, $8, $8)`,
		buildID, gitRepoID, envID, appName, commitSHA, attempt,
		time.Now().Add(-finishedAgo-time.Minute), time.Now().Add(-finishedAgo))
	t.Cleanup(func() {
		exec(t, pool, `DELETE FROM builds WHERE id = $1`, buildID)
	})
	return buildID
}

func buildRetryState(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (status string, failReason *string, attempt int) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT status, fail_reason, attempt FROM builds WHERE id = $1`, id,
	).Scan(&status, &failReason, &attempt); err != nil {
		t.Fatalf("read build %s: %v", id, err)
	}
	return status, failReason, attempt
}

// TestRetryPlatformFailedBuilds_RequeuesAndAudits is the case the 2026-08-13
// library-host outage left uncovered: the platform broke every build, the
// platform was fixed, and the users' builds stayed red because only the user
// could press Retry.
func TestRetryPlatformFailedBuilds_RequeuesAndAudits(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	ownerID := seedUser(t, pool)
	gitRepoID := seedGitRepoOwned(t, pool, projectID, envID, "app-platfail", ownerID)
	buildID := seedPlatformFailedBuild(t, pool, gitRepoID, envID, "app-platfail", "sha-platfail-"+uuid.New().String()[:8], time.Hour, 0)

	ids, err := RetryPlatformFailedBuilds(ctx, pool, 10*time.Minute, 24*time.Hour, PlatformRecoveryMaxAttempts)
	if err != nil {
		t.Fatalf("RetryPlatformFailedBuilds: %v", err)
	}
	if !containsID(ids, buildID) {
		t.Fatalf("retried ids = %v, want the platform-failed build %s", ids, buildID)
	}

	status, failReason, attempt := buildRetryState(t, pool, buildID)
	if status != "queued" {
		t.Errorf("status = %q, want queued", status)
	}
	if failReason != nil {
		t.Errorf("fail_reason = %q, want NULL (the row is in flight again)", *failReason)
	}
	if attempt != 1 {
		t.Errorf("attempt = %d, want 1", attempt)
	}

	var audits int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events
		  WHERE action = 'BuildAutoRetried' AND metadata->>'build_id' = $1`, buildID.String(),
	).Scan(&audits); err != nil {
		t.Fatalf("count BuildAutoRetried rows: %v", err)
	}
	if audits != 1 {
		t.Errorf("BuildAutoRetried rows = %d, want 1 (the retry must leave a trace)", audits)
	}
}

// TestRetryPlatformFailedBuilds_LeavesUserFailuresAndFreshOnesAlone pins the
// three ways the pass must stay quiet: a failure that was the user's code, a
// failure too young to distinguish from an outage still in progress, and a
// failure the user already superseded with a newer build.
func TestRetryPlatformFailedBuilds_LeavesUserFailuresAndFreshOnesAlone(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	ownerID := seedUser(t, pool)

	userRepo := seedGitRepoOwned(t, pool, projectID, envID, "app-usercode", ownerID)
	userBuild := seedPlatformFailedBuild(t, pool, userRepo, envID, "app-usercode", "sha-user-"+uuid.New().String()[:8], time.Hour, 0)
	exec(t, pool, `UPDATE builds SET fail_reason = 'dockerfile_build_failed' WHERE id = $1`, userBuild)

	freshRepo := seedGitRepoOwned(t, pool, projectID, envID, "app-fresh", ownerID)
	freshBuild := seedPlatformFailedBuild(t, pool, freshRepo, envID, "app-fresh", "sha-fresh-"+uuid.New().String()[:8], time.Minute, 0)

	supersededRepo := seedGitRepoOwned(t, pool, projectID, envID, "app-superseded", ownerID)
	oldBuild := seedPlatformFailedBuild(t, pool, supersededRepo, envID, "app-superseded", "sha-old-"+uuid.New().String()[:8], 2*time.Hour, 0)
	seedBuildStarted(t, pool, supersededRepo, envID, "app-superseded", "sha-new-"+uuid.New().String()[:8], "building", nil)

	exhaustedRepo := seedGitRepoOwned(t, pool, projectID, envID, "app-exhausted", ownerID)
	exhaustedBuild := seedPlatformFailedBuild(t, pool, exhaustedRepo, envID, "app-exhausted", "sha-exh-"+uuid.New().String()[:8], time.Hour, PlatformRecoveryMaxAttempts)

	ids, err := RetryPlatformFailedBuilds(ctx, pool, 10*time.Minute, 24*time.Hour, PlatformRecoveryMaxAttempts)
	if err != nil {
		t.Fatalf("RetryPlatformFailedBuilds: %v", err)
	}
	for name, id := range map[string]uuid.UUID{
		"user code failure":  userBuild,
		"failure too fresh":  freshBuild,
		"superseded failure": oldBuild,
		"attempts exhausted": exhaustedBuild,
	} {
		if containsID(ids, id) {
			t.Errorf("%s (%s) was re-queued, want left alone", name, id)
		}
		if status, _, _ := buildRetryState(t, pool, id); status != "failed" {
			t.Errorf("%s: status = %q, want failed", name, status)
		}
	}
}

// TestRetryPlatformFailedBuilds_RecoversBuildsThatExhaustedTheInflightBudget is
// the shape every real platform failure has: the in-flight retries burn the
// attempt budget within minutes of backoff, the outage lasts hours, and the
// build ends at attempt == maxBuildAttempts. On 2026-08-19 all 17 platform
// failures of the previous 14 days sat at exactly that number, so a recovery
// pass sharing that budget could never select a single one of them.
func TestRetryPlatformFailedBuilds_RecoversBuildsThatExhaustedTheInflightBudget(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	ownerID := seedUser(t, pool)
	repoID := seedGitRepoOwned(t, pool, projectID, envID, "app-burned", ownerID)
	buildID := seedPlatformFailedBuild(t, pool, repoID, envID, "app-burned", "sha-burned-"+uuid.New().String()[:8], time.Hour, InflightMaxAttempts)

	ids, err := RetryPlatformFailedBuilds(ctx, pool, 10*time.Minute, 24*time.Hour, PlatformRecoveryMaxAttempts)
	if err != nil {
		t.Fatalf("RetryPlatformFailedBuilds: %v", err)
	}
	if !containsID(ids, buildID) {
		t.Fatalf("retried ids = %v, want the build that burned its in-flight budget (%s)", ids, buildID)
	}
	if status, _, attempt := buildRetryState(t, pool, buildID); status != "queued" || attempt != InflightMaxAttempts+1 {
		t.Errorf("status = %q attempt = %d, want queued / %d", status, attempt, InflightMaxAttempts+1)
	}
}
