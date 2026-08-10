package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestMarkFailedWithReason_WritesBuildFinishedAudit pins the failure half of
// the terminal-verdict gap: before this, a failed build updated `builds` and
// left zero trace in audit_events, so a user path read as "TriggerBuild then
// silence" whether the build failed, hung, or the user gave up.
func TestMarkFailedWithReason_WritesBuildFinishedAudit(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	userID := seedUser(t, pool)
	gitRepoID := seedGitRepoOwned(t, pool, projectID, envID, "app-failed", userID)
	buildID := seedBuildStarted(t, pool, gitRepoID, envID, "app-failed", "sha-failed", "building", &userID)

	ok, err := MarkFailedWithReason(ctx, pool, buildID, StatusBuilding, "docker build exited 1", "dockerfile_build_failed")
	if err != nil {
		t.Fatalf("MarkFailedWithReason: %v", err)
	}
	if !ok {
		t.Fatal("MarkFailedWithReason reported no row changed")
	}

	row := readBuildFinishedAudit(t, pool, buildID)
	if row.count != 1 {
		t.Fatalf("BuildFinished rows for build = %d, want exactly 1", row.count)
	}
	if row.outcome != "failure" {
		t.Errorf("outcome = %q, want %q", row.outcome, "failure")
	}
	if row.actor != userID {
		t.Errorf("actor_id = %s, want the human who triggered the build (%s)", row.actor, userID)
	}
	if row.resourceName != "app-failed" {
		t.Errorf("resource_name = %q, want the app name", row.resourceName)
	}
	if row.projectID == nil || *row.projectID != projectID {
		t.Errorf("project_id = %v, want %s", row.projectID, projectID)
	}
	if row.envID == nil || *row.envID != envID {
		t.Errorf("environment_id = %v, want %s", row.envID, envID)
	}
	if row.failReason != "dockerfile_build_failed" {
		t.Errorf("metadata.fail_reason = %q, want %q", row.failReason, "dockerfile_build_failed")
	}
	if row.status != "failed" {
		t.Errorf("metadata.status = %q, want %q", row.status, "failed")
	}
}

// TestMarkFailedWithReason_NoDuplicateOnRetry proves the idempotency
// requirement: a second call against a build already moved to failed (e.g. a
// racing goroutine, or a caller retrying after a network blip on the first
// call's response) must change zero rows and therefore write zero additional
// audit rows -- the CAS on `from` and the CTE chain that only fires off the
// UPDATE's own RETURNING guarantee this without any separate dedupe key.
func TestMarkFailedWithReason_NoDuplicateOnRetry(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	userID := seedUser(t, pool)
	gitRepoID := seedGitRepoOwned(t, pool, projectID, envID, "app-retry", userID)
	buildID := seedBuildStarted(t, pool, gitRepoID, envID, "app-retry", "sha-retry", "building", &userID)

	ok1, err := MarkFailedWithReason(ctx, pool, buildID, StatusBuilding, "first failure", "platform_error")
	if err != nil || !ok1 {
		t.Fatalf("first MarkFailedWithReason: ok=%v err=%v", ok1, err)
	}
	ok2, err := MarkFailedWithReason(ctx, pool, buildID, StatusBuilding, "second failure", "platform_error")
	if err != nil {
		t.Fatalf("second MarkFailedWithReason: %v", err)
	}
	if ok2 {
		t.Fatal("second MarkFailedWithReason reported a row changed, want false (already failed)")
	}

	row := readBuildFinishedAudit(t, pool, buildID)
	if row.count != 1 {
		t.Fatalf("BuildFinished rows for build = %d, want exactly 1 (no duplicate on retry)", row.count)
	}
}

// TestFinishSuccess_WritesBuildFinishedAudit_PushBuildAttributesToOwner covers
// the push/webhook path: builds.triggered_by is NULL there (no human clicked
// anything), and per handoffActor's convention the row must still exist and
// attribute to the repo owner (git_repos.created_by), not be silently
// dropped -- an audit gap here is exactly the a.markov-buturskiy /
// dkazakova1810 blind spot this change closes.
func TestFinishSuccess_WritesBuildFinishedAudit_PushBuildAttributesToOwner(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	ownerID := seedUser(t, pool)
	gitRepoID := seedGitRepoOwned(t, pool, projectID, envID, "app-push", ownerID)
	buildID := seedBuildStarted(t, pool, gitRepoID, envID, "app-push", "sha-push", "pushing", nil)

	ok, err := FinishSuccess(ctx, pool, buildID, "harbor.example.com/p/app-push@sha256:deadbeef")
	if err != nil {
		t.Fatalf("FinishSuccess: %v", err)
	}
	if !ok {
		t.Fatal("FinishSuccess reported no row changed")
	}

	row := readBuildFinishedAudit(t, pool, buildID)
	if row.count != 1 {
		t.Fatalf("BuildFinished rows for build = %d, want exactly 1", row.count)
	}
	if row.outcome != "success" {
		t.Errorf("outcome = %q, want %q", row.outcome, "success")
	}
	if row.actor != ownerID {
		t.Errorf("actor_id = %s, want the repo owner %s (triggered_by is NULL for a push build)", row.actor, ownerID)
	}
	if row.status != "success" {
		t.Errorf("metadata.status = %q, want %q", row.status, "success")
	}
}

// TestFinishSuccess_OwnerlessRepoFallsBackToSystemActor covers the repo with
// no created_by at all (connected before migration 037): actor_id is NOT
// NULL on audit_events, so the row must still be written rather than
// silently dropped, landing on the system actor.
func TestFinishSuccess_OwnerlessRepoFallsBackToSystemActor(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	gitRepoID := seedGitRepo(t, pool, projectID, envID, "app-ownerless", "small")
	buildID := seedBuildStarted(t, pool, gitRepoID, envID, "app-ownerless", "sha-ownerless", "pushing", nil)

	ok, err := FinishSuccess(ctx, pool, buildID, "harbor.example.com/p/app-ownerless@sha256:cafe")
	if err != nil {
		t.Fatalf("FinishSuccess: %v", err)
	}
	if !ok {
		t.Fatal("FinishSuccess reported no row changed")
	}

	row := readBuildFinishedAudit(t, pool, buildID)
	if row.count != 1 {
		t.Fatalf("BuildFinished rows for build = %d, want exactly 1", row.count)
	}
	if row.actor != SystemUserID {
		t.Errorf("actor_id = %s, want the system user %s (repo has no owner and no triggered_by)", row.actor, SystemUserID)
	}
}

// TestMarkCanceled_WritesBuildFinishedAudit_OutcomeNotFailure pins the
// supersession semantics: the only caller of MarkCanceled is the runner's
// supersede() when a newer commit lands on the same repo+branch, a system
// decision, not something the user got wrong. Live data before this change:
// 24 canceled builds were all the same auto-superseded app, not user error --
// so outcome must read 'canceled', never 'failure'.
func TestMarkCanceled_WritesBuildFinishedAudit_OutcomeNotFailure(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	projectID, envID := seedProjectEnv(t, pool, "small")
	userID := seedUser(t, pool)
	gitRepoID := seedGitRepoOwned(t, pool, projectID, envID, "app-canceled", userID)
	buildID := seedBuildStarted(t, pool, gitRepoID, envID, "app-canceled", "sha-canceled", "building", &userID)

	ok, err := MarkCanceled(ctx, pool, buildID)
	if err != nil {
		t.Fatalf("MarkCanceled: %v", err)
	}
	if !ok {
		t.Fatal("MarkCanceled reported no row changed")
	}

	row := readBuildFinishedAudit(t, pool, buildID)
	if row.count != 1 {
		t.Fatalf("BuildFinished rows for build = %d, want exactly 1", row.count)
	}
	if row.outcome != "canceled" {
		t.Errorf("outcome = %q, want %q (a system supersession is not a user failure)", row.outcome, "canceled")
	}
	if row.status != "canceled" {
		t.Errorf("metadata.status = %q, want %q", row.status, "canceled")
	}
}

func seedGitRepoOwned(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName string, ownerID uuid.UUID) uuid.UUID {
	t.Helper()
	gitRepoID := uuid.New()
	exec(t, pool,
		`INSERT INTO git_repos (id, project_id, environment_id, app_name, provider, repo_full_name, clone_url, created_by)
		 VALUES ($1, $2, $3, $4, 'github', $5, $6, $7)`,
		gitRepoID, projectID, envID, appName, "org/"+appName, "https://example.com/org/"+appName+".git", ownerID)
	return gitRepoID
}

// seedBuildStarted seeds a build already in an in-flight status with
// started_at set, as ClaimQueued would have done, so the terminal-write CAS
// under test has a real row to transition from.
func seedBuildStarted(t *testing.T, pool *pgxpool.Pool, gitRepoID, envID uuid.UUID, appName, commitSHA, status string, triggeredBy *uuid.UUID) uuid.UUID {
	t.Helper()
	buildID := uuid.New()
	trigger := "push"
	if triggeredBy != nil {
		trigger = "manual"
	}
	exec(t, pool,
		`INSERT INTO builds (id, git_repo_id, environment_id, app_name, commit_sha, branch, triggered_by, trigger, status, started_at)
		 VALUES ($1, $2, $3, $4, $5, 'main', $6, $7, $8, $9)`,
		buildID, gitRepoID, envID, appName, commitSHA, triggeredBy, trigger, status, time.Now().Add(-30*time.Second))
	return buildID
}

type buildFinishedAuditRow struct {
	count        int
	outcome      string
	actor        uuid.UUID
	resourceName string
	projectID    *uuid.UUID
	envID        *uuid.UUID
	status       string
	failReason   string
}

func readBuildFinishedAudit(t *testing.T, pool *pgxpool.Pool, buildID uuid.UUID) buildFinishedAuditRow {
	t.Helper()
	var out buildFinishedAuditRow
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events
		 WHERE action = 'BuildFinished' AND metadata->>'build_id' = $1`, buildID.String(),
	).Scan(&out.count); err != nil {
		t.Fatalf("count BuildFinished rows: %v", err)
	}
	if out.count == 0 {
		return out
	}
	var failReason *string
	if err := pool.QueryRow(context.Background(),
		`SELECT outcome, actor_id, resource_name, project_id, environment_id,
		        metadata->>'status', metadata->>'fail_reason'
		   FROM audit_events
		  WHERE action = 'BuildFinished' AND metadata->>'build_id' = $1
		  LIMIT 1`, buildID.String(),
	).Scan(&out.outcome, &out.actor, &out.resourceName, &out.projectID, &out.envID, &out.status, &failReason); err != nil {
		t.Fatalf("read BuildFinished row: %v", err)
	}
	if failReason != nil {
		out.failReason = *failReason
	}
	return out
}
