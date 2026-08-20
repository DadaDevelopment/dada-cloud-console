package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedPlatformSelfHealGitRepo inserts one git_repos row linking (projectID,
// envID, appName) to a github-provider repo, the shape platformSelfHealCandidates
// requires an app to have before it is rebuildable.
func seedPlatformSelfHealGitRepo(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO git_repos (project_id, environment_id, app_name, provider, repo_full_name, clone_url, production_branch)
		 VALUES ($1, $2, $3, 'github', $4, $5, 'main') RETURNING id`,
		projectID, envID, appName, "acme/"+appName, "https://github.com/acme/"+appName+".git",
	).Scan(&id); err != nil {
		t.Fatalf("seed git_repos for %s: %v", appName, err)
	}
	return id
}

// seedPlatformSelfHealAlert inserts one app_health_alerts row with an
// explicit last_seen_at age and cause_kind, and optionally an already-claimed
// selfheal_rebuilt_at, so a test can reproduce every shape
// platformSelfHealCandidates has to tell apart.
func seedPlatformSelfHealAlert(t *testing.T, pool *pgxpool.Pool, namespace, appName, causeKind string, lastSeenAge time.Duration, alreadyRebuilt bool) {
	t.Helper()
	var rebuiltAt any
	var rebuiltCause any
	if alreadyRebuilt {
		rebuiltAt = time.Now().Add(-time.Hour)
		rebuiltCause = causeKind
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO app_health_alerts (namespace, app_name, last_sent_at, last_seen_at, reason, cause_kind, selfheal_rebuilt_at, selfheal_rebuilt_cause_kind)
		 VALUES ($1, $2, now(), now() - $3::interval, 'CrashLoopBackOff', $4, $5, $6)
		 ON CONFLICT (namespace, app_name) DO UPDATE SET
		   last_seen_at = EXCLUDED.last_seen_at, cause_kind = EXCLUDED.cause_kind,
		   selfheal_rebuilt_at = EXCLUDED.selfheal_rebuilt_at,
		   selfheal_rebuilt_cause_kind = EXCLUDED.selfheal_rebuilt_cause_kind`,
		namespace, appName, lastSeenAge.String(), causeKind, rebuiltAt, rebuiltCause,
	); err != nil {
		t.Fatalf("seed app_health_alerts for %s/%s: %v", namespace, appName, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_health_alerts WHERE namespace = $1 AND app_name = $2`, namespace, appName)
	})
}

// seedPlatformSelfHealBuild inserts one terminal build row for gitRepoID, so
// a test can prove the "already rebuilt after the fix landed" exclusion.
func seedPlatformSelfHealBuild(t *testing.T, pool *pgxpool.Pool, envID, gitRepoID uuid.UUID, appName, status string, finishedAge time.Duration) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO builds (git_repo_id, environment_id, app_name, commit_sha, branch, trigger, status, finished_at)
		 VALUES ($1, $2, $3, $4, 'main', 'manual', $5, now() - $6::interval)`,
		gitRepoID, envID, appName, "sha-"+uuid.NewString()[:8], status, finishedAge.String(),
	); err != nil {
		t.Fatalf("seed build for %s: %v", appName, err)
	}
}

// TestPlatformSelfHealCandidates_SelectsOnlyEligibleApps is the RED-provable
// proof for the sweeper's candidate query: an app must be currently unhealthy
// on the exact signature, backed by a git repo, running an image older than
// the fix, and not yet claimed -- all four at once. Each of the other five
// seeded apps fails exactly one of those and must be excluded.
func TestPlatformSelfHealCandidates_SelectsOnlyEligibleApps(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	projectID := overviewBrokenSeedProject(t, pool, "selfheal-cand-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")
	var ns string
	if err := pool.QueryRow(context.Background(), `SELECT namespace FROM environments WHERE id = $1`, envID).Scan(&ns); err != nil {
		t.Fatalf("read seeded namespace: %v", err)
	}

	fix := selfHealFix{
		CauseKind: "app_entrypoint_import",
		FixedAt:   time.Now().Add(-24 * time.Hour),
		Note:      "test fix",
	}

	eligible := "eligible-" + suffix
	seedPlatformSelfHealGitRepo(t, pool, projectID, envID, eligible)
	seedPlatformSelfHealAlert(t, pool, ns, eligible, fix.CauseKind, 1*time.Minute, false)

	rebuiltAfterFix := "rebuilt-after-fix-" + suffix
	rebuiltGitRepo := seedPlatformSelfHealGitRepo(t, pool, projectID, envID, rebuiltAfterFix)
	seedPlatformSelfHealAlert(t, pool, ns, rebuiltAfterFix, fix.CauseKind, 1*time.Minute, false)
	seedPlatformSelfHealBuild(t, pool, envID, rebuiltGitRepo, rebuiltAfterFix, "success", 1*time.Hour)

	noRepo := "no-repo-" + suffix
	seedPlatformSelfHealAlert(t, pool, ns, noRepo, fix.CauseKind, 1*time.Minute, false)

	alreadyClaimed := "already-claimed-" + suffix
	seedPlatformSelfHealGitRepo(t, pool, projectID, envID, alreadyClaimed)
	seedPlatformSelfHealAlert(t, pool, ns, alreadyClaimed, fix.CauseKind, 1*time.Minute, true)

	otherCause := "other-cause-" + suffix
	seedPlatformSelfHealGitRepo(t, pool, projectID, envID, otherCause)
	seedPlatformSelfHealAlert(t, pool, ns, otherCause, "db_read_only", 1*time.Minute, false)

	stale := "stale-" + suffix
	seedPlatformSelfHealGitRepo(t, pool, projectID, envID, stale)
	seedPlatformSelfHealAlert(t, pool, ns, stale, fix.CauseKind, appHealthAlertFreshWindow+time.Hour, false)

	candidates, err := h.platformSelfHealCandidates(context.Background(), fix)
	if err != nil {
		t.Fatalf("platformSelfHealCandidates: %v", err)
	}

	got := map[string]bool{}
	for _, c := range candidates {
		if c.Namespace == ns {
			got[c.AppName] = true
		}
	}

	if !got[eligible] {
		t.Errorf("expected %q to be selected, was not among %v", eligible, got)
	}
	for _, excluded := range []string{rebuiltAfterFix, noRepo, alreadyClaimed, otherCause, stale} {
		if got[excluded] {
			t.Errorf("expected %q to be EXCLUDED, but it was selected", excluded)
		}
	}
}

// TestAttemptPlatformSelfHeal_ExactlyOneAttemptAndAuditTrail proves the two
// load-bearing guarantees of attemptPlatformSelfHeal: calling it twice for
// the same app/signature queues exactly one build (the claim in
// claimPlatformSelfHealSlot must make the second call a no-op), and the
// attempt leaves a joinable intent+verdict audit pair (pending, then
// success) sharing one OperationID.
func TestAttemptPlatformSelfHeal_ExactlyOneAttemptAndAuditTrail(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	projectID := overviewBrokenSeedProject(t, pool, "selfheal-attempt-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")
	var ns string
	if err := pool.QueryRow(context.Background(), `SELECT namespace FROM environments WHERE id = $1`, envID).Scan(&ns); err != nil {
		t.Fatalf("read seeded namespace: %v", err)
	}
	appName := "healme-" + suffix
	gitRepoID := seedPlatformSelfHealGitRepo(t, pool, projectID, envID, appName)
	seedPlatformSelfHealAlert(t, pool, ns, appName, "app_entrypoint_import", 1*time.Minute, false)

	fix := selfHealFix{CauseKind: "app_entrypoint_import", FixedAt: time.Now().Add(-24 * time.Hour), Note: "test fix"}
	cand := platformSelfHealCandidate{
		Namespace: ns, AppName: appName, ProjectID: projectID, EnvironmentID: envID,
		GitRepoID: gitRepoID, Branch: "main", Provider: "github",
	}

	h.attemptPlatformSelfHeal(context.Background(), fix, cand)
	h.attemptPlatformSelfHeal(context.Background(), fix, cand)

	var buildCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM builds WHERE git_repo_id = $1`, gitRepoID,
	).Scan(&buildCount); err != nil {
		t.Fatalf("count builds: %v", err)
	}
	if buildCount != 1 {
		t.Fatalf("builds queued for %s = %d, want exactly 1 (second attemptPlatformSelfHeal call must be a no-op)", appName, buildCount)
	}

	var rebuiltAt *time.Time
	var rebuiltCause *string
	if err := pool.QueryRow(context.Background(),
		`SELECT selfheal_rebuilt_at, selfheal_rebuilt_cause_kind FROM app_health_alerts WHERE namespace = $1 AND app_name = $2`,
		ns, appName,
	).Scan(&rebuiltAt, &rebuiltCause); err != nil {
		t.Fatalf("read back app_health_alerts: %v", err)
	}
	if rebuiltAt == nil {
		t.Fatal("expected selfheal_rebuilt_at to be stamped after one attempt")
	}
	if rebuiltCause == nil || *rebuiltCause != fix.CauseKind {
		t.Fatalf("selfheal_rebuilt_cause_kind = %v, want %q", rebuiltCause, fix.CauseKind)
	}

	rows, err := pool.Query(context.Background(),
		`SELECT outcome, metadata->>'unresolved_operation_id', metadata->>'cause_kind'
		 FROM audit_events WHERE action = $1 AND resource_name = $2 ORDER BY created_at ASC`,
		auditActionPlatformSelfHealRebuild, appName,
	)
	if err != nil {
		t.Fatalf("query audit_events: %v", err)
	}
	defer rows.Close()

	type row struct{ outcome, opID, causeKind string }
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.outcome, &r.opID, &r.causeKind); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		got = append(got, r)
	}
	if len(got) != 2 {
		t.Fatalf("audit_events rows for %s = %d, want exactly 2 (intent + verdict)", appName, len(got))
	}
	if got[0].outcome != auditOutcomePending {
		t.Errorf("first audit row outcome = %q, want %q (intent must precede the build insert)", got[0].outcome, auditOutcomePending)
	}
	if got[1].outcome != auditOutcomeSuccess {
		t.Errorf("second audit row outcome = %q, want %q", got[1].outcome, auditOutcomeSuccess)
	}
	if got[0].opID == "" || got[0].opID != got[1].opID {
		t.Errorf("intent/verdict rows do not share an operation id: %q vs %q", got[0].opID, got[1].opID)
	}
	if got[0].causeKind != fix.CauseKind || got[1].causeKind != fix.CauseKind {
		t.Errorf("audit metadata cause_kind = %q/%q, want both %q", got[0].causeKind, got[1].causeKind, fix.CauseKind)
	}
}
