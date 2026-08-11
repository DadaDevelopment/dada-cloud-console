package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// reapWindow is far above any started_at this file seeds for a "still live"
// build, so the reaper under test can only ever pick up the rows a subtest
// deliberately aged past it -- including rows other tests in this package
// leave behind in an in-flight status.
const reapWindow = time.Hour

// TestReapStuckBuilds_WritesBuildFinishedAudit closes the last hole of the
// terminal-verdict contract (530defbb covered MarkFailedWithReason /
// MarkCanceled / FinishSuccess). A build killed by an agent restart is the one
// outcome the user cannot distinguish from "still running": without this row
// their path reads TriggerBuild then silence forever.
func TestReapStuckBuilds_WritesBuildFinishedAudit(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	ownerID := seedUser(t, pool)
	gitRepoID := seedGitRepoOwned(t, pool, projectID, envID, "app-reaped", ownerID)
	buildID := seedBuildStarted(t, pool, gitRepoID, envID, "app-reaped", "sha-reaped", "building", nil)
	ageBuild(t, pool, buildID, 3*time.Hour)

	ids, err := ReapStuckBuilds(ctx, pool, reapWindow)
	if err != nil {
		t.Fatalf("ReapStuckBuilds: %v", err)
	}
	if !containsID(ids, buildID) {
		t.Fatalf("reaped ids = %v, want the orphaned build %s", ids, buildID)
	}

	row := readBuildFinishedAudit(t, pool, buildID)
	if row.count != 1 {
		t.Fatalf("BuildFinished rows for reaped build = %d, want exactly 1", row.count)
	}
	if row.outcome != "failure" {
		t.Errorf("outcome = %q, want %q", row.outcome, "failure")
	}
	if row.actor != ownerID {
		t.Errorf("actor_id = %s, want the repo owner %s (a reaped build has no triggering human)", row.actor, ownerID)
	}
	if row.resourceName != "app-reaped" {
		t.Errorf("resource_name = %q, want the app name", row.resourceName)
	}
	if row.projectID == nil || *row.projectID != projectID {
		t.Errorf("project_id = %v, want %s", row.projectID, projectID)
	}
	if row.envID == nil || *row.envID != envID {
		t.Errorf("environment_id = %v, want %s", row.envID, envID)
	}
	if row.failReason != "platform_error" {
		t.Errorf("metadata.fail_reason = %q, want %q -- an orphaned build is our fault, not the user's", row.failReason, "platform_error")
	}
	if row.status != "failed" {
		t.Errorf("metadata.status = %q, want %q", row.status, "failed")
	}
	if !auditMetadataFlag(t, pool, buildID, "reaped") {
		t.Error("metadata.reaped is not true -- a restart-orphaned build must be distinguishable from a build that genuinely failed")
	}
}

// TestReapStuckBuilds_LiveBuildUntouched is the control: the audit write must
// ride on the same predicate as the status change, so a build inside its
// window gets neither a terminal status nor a verdict row.
func TestReapStuckBuilds_LiveBuildUntouched(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	ownerID := seedUser(t, pool)
	gitRepoID := seedGitRepoOwned(t, pool, projectID, envID, "app-live", ownerID)
	buildID := seedBuildStarted(t, pool, gitRepoID, envID, "app-live", "sha-live", "building", nil)

	ids, err := ReapStuckBuilds(ctx, pool, reapWindow)
	if err != nil {
		t.Fatalf("ReapStuckBuilds: %v", err)
	}
	if containsID(ids, buildID) {
		t.Fatalf("live build %s was reaped", buildID)
	}
	if row := readBuildFinishedAudit(t, pool, buildID); row.count != 0 {
		t.Fatalf("BuildFinished rows for live build = %d, want 0", row.count)
	}
	if got := buildStatus(t, pool, buildID); got != "building" {
		t.Fatalf("status = %q, want %q", got, "building")
	}
}

// TestReapStuckBuilds_FansOutOverEveryReapedRow pins the difference from the
// single-row terminal writers: one reaper pass fails a SET of builds, and each
// of them owns a user-visible verdict. A CTE that collapsed to one row would
// silently drop every build but one.
func TestReapStuckBuilds_FansOutOverEveryReapedRow(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	ownerID := seedUser(t, pool)

	var builds []uuid.UUID
	for _, name := range []string{"app-fan-a", "app-fan-b", "app-fan-c"} {
		gitRepoID := seedGitRepoOwned(t, pool, projectID, envID, name, ownerID)
		id := seedBuildStarted(t, pool, gitRepoID, envID, name, "sha-"+name, "pushing", nil)
		ageBuild(t, pool, id, 3*time.Hour)
		builds = append(builds, id)
	}

	ids, err := ReapStuckBuilds(ctx, pool, reapWindow)
	if err != nil {
		t.Fatalf("ReapStuckBuilds: %v", err)
	}
	for _, id := range builds {
		if !containsID(ids, id) {
			t.Errorf("build %s was not reaped", id)
		}
		if row := readBuildFinishedAudit(t, pool, id); row.count != 1 {
			t.Errorf("BuildFinished rows for %s = %d, want exactly 1", id, row.count)
		}
	}
}

// TestReapStuckBuilds_NoDuplicateOnSecondPass proves idempotency the same way
// the CAS writers get it: the reaper runs on every poll tick, and a row it
// already moved out of the in-flight statuses can no longer match, so its CTE
// chain inserts nothing.
func TestReapStuckBuilds_NoDuplicateOnSecondPass(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	ownerID := seedUser(t, pool)
	gitRepoID := seedGitRepoOwned(t, pool, projectID, envID, "app-twice", ownerID)
	buildID := seedBuildStarted(t, pool, gitRepoID, envID, "app-twice", "sha-twice", "detecting", nil)
	ageBuild(t, pool, buildID, 3*time.Hour)

	if _, err := ReapStuckBuilds(ctx, pool, reapWindow); err != nil {
		t.Fatalf("first ReapStuckBuilds: %v", err)
	}
	ids, err := ReapStuckBuilds(ctx, pool, reapWindow)
	if err != nil {
		t.Fatalf("second ReapStuckBuilds: %v", err)
	}
	if containsID(ids, buildID) {
		t.Fatalf("build %s reaped twice", buildID)
	}
	if row := readBuildFinishedAudit(t, pool, buildID); row.count != 1 {
		t.Fatalf("BuildFinished rows after two passes = %d, want exactly 1", row.count)
	}
}

func ageBuild(t *testing.T, pool *pgxpool.Pool, buildID uuid.UUID, age time.Duration) {
	t.Helper()
	exec(t, pool, `UPDATE builds SET started_at = NOW() - make_interval(secs => $2) WHERE id = $1`,
		buildID, age.Seconds())
}

func buildStatus(t *testing.T, pool *pgxpool.Pool, buildID uuid.UUID) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM builds WHERE id = $1`, buildID).Scan(&status); err != nil {
		t.Fatalf("read build status: %v", err)
	}
	return status
}

func auditMetadataFlag(t *testing.T, pool *pgxpool.Pool, buildID uuid.UUID, key string) bool {
	t.Helper()
	var flag *bool
	if err := pool.QueryRow(context.Background(),
		`SELECT (metadata->>$2)::bool FROM audit_events
		  WHERE action = 'BuildFinished' AND metadata->>'build_id' = $1 LIMIT 1`,
		buildID.String(), key,
	).Scan(&flag); err != nil {
		t.Fatalf("read audit metadata %s: %v", key, err)
	}
	return flag != nil && *flag
}

func containsID(ids []uuid.UUID, want uuid.UUID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
