package api

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// overviewBrokenTestPool mirrors the pool-backed test harness used across the
// repo (alert_recipient_test.go, admin_overview_phase_null_test.go): skip
// cleanly when TEST_DATABASE_URL is unset so `go test ./...` stays green
// offline.
func overviewBrokenTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping admin-overview broken-state DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func overviewBrokenSeedProject(t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`, name,
	).Scan(&id); err != nil {
		t.Fatalf("seed project %s: %v", name, err)
	}
	t.Cleanup(func() { dropSeededProject(pool, id) })
	return id
}

func overviewBrokenSeedEnv(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, $2, $3, 'prod') RETURNING id`,
		projectID, name, "ns-"+name,
	).Scan(&id); err != nil {
		t.Fatalf("seed environment %s: %v", name, err)
	}
	return id
}

func overviewBrokenSeedUser(t *testing.T, pool *pgxpool.Pool, username, email string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, display_name) VALUES ($1, $2, '', $1) RETURNING id`,
		username, email,
	).Scan(&id); err != nil {
		t.Fatalf("seed user %s: %v", username, err)
	}
	t.Cleanup(func() { dropSeededUser(pool, id) })
	return id
}

// TestOverviewNotReadyFreshnessDetectsBlindness reproduces the "gitops-agent
// died and the panel reads as all-green" scenario the owner flagged as the
// most dangerous gap: brokenAppSnapshotPredicate requires a snapshot inside
// the last 10 minutes, so once every App snapshot ages out, overviewNotReadyApps
// legitimately returns zero rows. overviewNotReadyFreshness must be the thing
// that tells the operator the zero means "nothing is watching", not
// "everything is fine".
func TestOverviewNotReadyFreshnessDetectsBlindness(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	projectID := overviewBrokenSeedProject(t, pool, "fresh-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	staleAt := time.Now().Add(-40 * time.Minute)
	_, err := pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, 'App', $3, 'CrashLoopBackOff', '{"live_source":"k8s"}', $4)`,
		projectID, envID, "app-"+suffix, staleAt,
	)
	if err != nil {
		t.Fatalf("seed stale app snapshot: %v", err)
	}

	notReady, err := h.overviewNotReadyApps(context.Background())
	if err != nil {
		t.Fatalf("overviewNotReadyApps: %v", err)
	}
	for _, a := range notReady {
		if a.Name == "app-"+suffix {
			t.Fatalf("a snapshot last synced 40 minutes ago must not appear in the not-ready list (freshness window is 10 minutes)")
		}
	}

	freshness, err := h.overviewNotReadyFreshness(context.Background())
	if err != nil {
		t.Fatalf("overviewNotReadyFreshness: %v", err)
	}
	if freshness.StaleApps < 1 {
		t.Fatalf("StaleApps = %d, want at least 1 (the app snapshot seeded 40 minutes ago)", freshness.StaleApps)
	}
	if freshness.NewestSyncAgeSeconds == nil {
		t.Fatal("NewestSyncAgeSeconds = nil, want a value once at least one App/k8s snapshot exists")
	}
	if !freshness.Blind {
		t.Fatal("Blind = false, want true: the only App snapshot in the fixture is 40 minutes stale, so the reconciler must be presumed dead")
	}
}

// TestOverviewNotReadyFreshnessHealthy is the control case: a snapshot
// synced seconds ago must read as NOT blind, so the freshness flag does not
// cry wolf on a healthy reconciler.
func TestOverviewNotReadyFreshnessHealthy(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	projectID := overviewBrokenSeedProject(t, pool, "healthy-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	_, err := pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, 'App', $3, 'Ready', '{"live_source":"k8s"}', now())`,
		projectID, envID, "app-"+suffix,
	)
	if err != nil {
		t.Fatalf("seed fresh app snapshot: %v", err)
	}

	freshness, err := h.overviewNotReadyFreshness(context.Background())
	if err != nil {
		t.Fatalf("overviewNotReadyFreshness: %v", err)
	}
	if freshness.Blind {
		t.Fatal("Blind = true, want false: a snapshot synced moments ago proves the reconciler is alive")
	}
}

// TestOverviewNotReadyOtherResources checks the non-k8s section: a
// ServiceDatabaseV2 (live_source=crossplane) stuck out of Ready must show up,
// while a k8s App and an orphan-GC-cleared row must not.
func TestOverviewNotReadyOtherResources(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	projectID := overviewBrokenSeedProject(t, pool, "otherkind-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	seed := func(kind, name, phase, liveSource string) {
		_, err := pool.Exec(context.Background(),
			`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json)
			 VALUES ($1, $2, $3, $4, $5, jsonb_build_object('live_source', $6::text))`,
			projectID, envID, kind, name, phase, liveSource,
		)
		if err != nil {
			t.Fatalf("seed %s snapshot: %v", kind, err)
		}
	}

	seed("ServiceDatabaseV2", "db-broken-"+suffix, "Failed", "crossplane")
	seed("ServiceDatabaseV2", "db-ready-"+suffix, "Ready", "crossplane")
	seed("App", "app-"+suffix, "CrashLoopBackOff", "k8s")
	seed("ServiceDatabaseV2", "db-gc-"+suffix, "Pending", "orphan-gc-cleared")

	out, err := h.overviewNotReadyOtherResources(context.Background())
	if err != nil {
		t.Fatalf("overviewNotReadyOtherResources: %v", err)
	}

	names := map[string]bool{}
	for _, r := range out {
		names[r.Name] = true
	}
	if !names["db-broken-"+suffix] {
		t.Fatal("expected the broken ServiceDatabaseV2 (crossplane, phase Failed) in the list")
	}
	if names["db-ready-"+suffix] {
		t.Fatal("a Ready database must not appear as not-ready")
	}
	if names["app-"+suffix] {
		t.Fatal("a k8s App must not appear in the non-k8s section, it belongs in overviewNotReadyApps")
	}
	if names["db-gc-"+suffix] {
		t.Fatal("an orphan-gc-cleared row must not appear as currently broken")
	}
}

// TestOverviewNotReadyAppsExposesReason reproduces the smart-tender-ai-site
// incident (2026-08-13): the gitops-agent status reconciler's livePhase
// folds CrashLoopBackOff, ImagePullBackOff, ErrImagePull and OOMKilled into
// the single phase string "CrashLoop" so the console's phase badge has one
// red state to render (see livePhase in
// gitops-agent/internal/worker/statusreconciler.go). That collapse is
// correct for the badge but, before this fix, was the only signal the admin
// overview panel exposed -- an operator opening the not-ready list could not
// tell an image the registry never delivered (our fault) from an app that
// genuinely crashes on every start (the owner's fault). The reconciler
// already stamps the specific kube waiting reason into summary_json's
// "reason" key on the same patch that sets phase, so this only requires
// reading it back.
func TestOverviewNotReadyAppsExposesReason(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	projectID := overviewBrokenSeedProject(t, pool, "reason-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	seed := func(name, reason string) {
		_, err := pool.Exec(context.Background(),
			`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
			 VALUES ($1, $2, 'App', $3, 'CrashLoop', jsonb_build_object('live_source', 'k8s', 'reason', $4::text), now())`,
			projectID, envID, name, reason,
		)
		if err != nil {
			t.Fatalf("seed app snapshot %s: %v", name, err)
		}
	}

	imagePullApp := "imagepull-" + suffix
	crashLoopApp := "crashloop-" + suffix
	seed(imagePullApp, "ImagePullBackOff")
	seed(crashLoopApp, "CrashLoopBackOff")

	out, err := h.overviewNotReadyApps(context.Background())
	if err != nil {
		t.Fatalf("overviewNotReadyApps: %v", err)
	}

	byName := map[string]overviewNotReadyApp{}
	for _, a := range out {
		byName[a.Name] = a
	}

	imagePull, ok := byName[imagePullApp]
	if !ok {
		t.Fatalf("expected %s in the not-ready list", imagePullApp)
	}
	if imagePull.Phase != "CrashLoop" {
		t.Fatalf("Phase = %q, want %q (the reconciler collapses all bad waiting reasons into this one phase)", imagePull.Phase, "CrashLoop")
	}
	if imagePull.Reason != "ImagePullBackOff" {
		t.Fatalf("Reason = %q, want %q -- the panel must be able to tell a registry failure apart from a real crash loop even though Phase reads the same for both", imagePull.Reason, "ImagePullBackOff")
	}

	crashLoop, ok := byName[crashLoopApp]
	if !ok {
		t.Fatalf("expected %s in the not-ready list", crashLoopApp)
	}
	if crashLoop.Reason != "CrashLoopBackOff" {
		t.Fatalf("Reason = %q, want %q", crashLoop.Reason, "CrashLoopBackOff")
	}
	if imagePull.Reason == crashLoop.Reason {
		t.Fatal("an image-pull failure and a genuine crash loop must not report the same Reason")
	}
}

// TestOverviewDomainIssues checks both stages: a failed hostname and a
// stale (>1 day) pending authorization must both surface, while a healthy
// active hostname must not.
func TestOverviewDomainIssues(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	userID := overviewBrokenSeedUser(t, pool, "domainowner-"+suffix, "domainowner-"+suffix+"@example.test")
	projectID := overviewBrokenSeedProject(t, pool, "domainissues-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	var authID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO domain_authorizations (project_id, apex_domain, verification_token, status, created_by, created_at, updated_at)
		 VALUES ($1, $2, $3, 'pending', $4, now() - interval '2 days', now() - interval '2 days')
		 RETURNING id`,
		projectID, "stale-"+suffix+".test", "tok-stale-"+suffix, userID,
	).Scan(&authID); err != nil {
		t.Fatalf("seed stale authorization: %v", err)
	}

	var authIDFresh uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO domain_authorizations (project_id, apex_domain, verification_token, status, created_by)
		 VALUES ($1, $2, $3, 'verified', $4) RETURNING id`,
		projectID, "verified-"+suffix+".test", "tok-verified-"+suffix, userID,
	).Scan(&authIDFresh); err != nil {
		t.Fatalf("seed verified authorization: %v", err)
	}

	if _, err := pool.Exec(context.Background(),
		`INSERT INTO domain_hostnames (authorization_id, environment_id, app_name, hostname, record_type, status, cert_status)
		 VALUES ($1, $2, 'app', $3, 'CNAME', 'failed', 'failed')`,
		authIDFresh, envID, "failed-"+suffix+".test",
	); err != nil {
		t.Fatalf("seed failed hostname: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO domain_hostnames (authorization_id, environment_id, app_name, hostname, record_type, status, cert_status)
		 VALUES ($1, $2, 'app', $3, 'CNAME', 'active', 'active')`,
		authIDFresh, envID, "active-"+suffix+".test",
	); err != nil {
		t.Fatalf("seed active hostname: %v", err)
	}

	issues, err := h.overviewDomainIssues(context.Background())
	if err != nil {
		t.Fatalf("overviewDomainIssues: %v", err)
	}

	byHostname := map[string]overviewDomainIssue{}
	for _, i := range issues {
		byHostname[i.Hostname] = i
	}
	if _, ok := byHostname["failed-"+suffix+".test"]; !ok {
		t.Fatal("expected the failed hostname to be reported")
	}
	if _, ok := byHostname["active-"+suffix+".test"]; ok {
		t.Fatal("an active hostname must not be reported as an issue")
	}
	if _, ok := byHostname["stale-"+suffix+".test"]; !ok {
		t.Fatal("expected the 2-day-old pending authorization to be reported as stuck")
	}
	if _, ok := byHostname["verified-"+suffix+".test"]; ok {
		t.Fatal("a verified authorization must not be reported as an issue")
	}
}

// TestOverviewStuckOperations checks that an operation stuck in a
// non-terminal status past stuckOperationThreshold is reported, a
// fresh non-terminal one is not, and a terminal one (even if old) is not.
func TestOverviewStuckOperations(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	userID := overviewBrokenSeedUser(t, pool, "opsowner-"+suffix, "opsowner-"+suffix+"@example.test")
	projectID := overviewBrokenSeedProject(t, pool, "stuckops-"+suffix)

	seedOp := func(status string, age time.Duration, resourceName string) uuid.UUID {
		var id uuid.UUID
		createdAt := time.Now().Add(-age)
		if err := pool.QueryRow(context.Background(),
			`INSERT INTO operations (actor_id, project_id, action, resource_kind, resource_name, status, created_at, updated_at)
			 VALUES ($1, $2, 'CreateApp', 'App', $3, $4, $5, $5) RETURNING id`,
			userID, projectID, resourceName, status, createdAt,
		).Scan(&id); err != nil {
			t.Fatalf("seed operation %s: %v", resourceName, err)
		}
		return id
	}

	stuckID := seedOp("Reconciling", stuckOperationThreshold+5*time.Minute, "stuck-"+suffix)
	seedOp("Reconciling", 1*time.Minute, "fresh-"+suffix)
	seedOp("Ready", stuckOperationThreshold+time.Hour, "old-but-done-"+suffix)
	seedOp("Committed", stuckOperationThreshold+time.Hour, "committed-"+suffix)
	seedOp("WaitingForApproval", stuckOperationThreshold+time.Hour, "awaiting-human-"+suffix)

	out, err := h.overviewStuckOperations(context.Background())
	if err != nil {
		t.Fatalf("overviewStuckOperations: %v", err)
	}

	found := false
	for _, op := range out.Oldest {
		if op.ID == stuckID.String() {
			found = true
		}
		if op.ResourceName == "fresh-"+suffix {
			t.Fatal("a non-terminal operation younger than stuckOperationThreshold must not be reported as stuck")
		}
		if op.ResourceName == "old-but-done-"+suffix {
			t.Fatal("a terminal (Ready) operation must never be reported as stuck regardless of age")
		}
		if op.ResourceName == "committed-"+suffix {
			t.Fatal("Committed is where gitops-agent finishes an operation; reporting it as stuck is what made every finished deploy a false alarm")
		}
		if op.ResourceName == "awaiting-human-"+suffix {
			t.Fatal("WaitingForApproval is parked on a human by design, not on a dead agent")
		}
	}
	if !found {
		t.Fatalf("expected the operation stuck in Reconciling past the threshold to be in the oldest list, got %+v", out.Oldest)
	}
	if out.Count < 1 {
		t.Fatalf("Count = %d, want at least 1", out.Count)
	}
}

// TestOverviewFailedLatestBuilds checks that an app whose MOST RECENT build
// failed is reported even though an earlier build for the same app
// succeeded, and that an app whose latest build succeeded is not reported.
func TestOverviewFailedLatestBuilds(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	userID := overviewBrokenSeedUser(t, pool, "buildowner-"+suffix, "buildowner-"+suffix+"@example.test")
	_ = userID
	projectID := overviewBrokenSeedProject(t, pool, "failedbuilds-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	seedRepo := func(appName string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(context.Background(),
			`INSERT INTO git_repos (project_id, environment_id, app_name, provider, repo_full_name, clone_url)
			 VALUES ($1, $2, $3, 'github', $4, $5) RETURNING id`,
			projectID, envID, appName, "org/"+appName, "https://example.test/"+appName+".git",
		).Scan(&id); err != nil {
			t.Fatalf("seed git repo %s: %v", appName, err)
		}
		return id
	}
	seedBuild := func(repoID uuid.UUID, sha, status string, createdAt time.Time) {
		if _, err := pool.Exec(context.Background(),
			`INSERT INTO builds (git_repo_id, environment_id, app_name, commit_sha, branch, status, created_at, updated_at)
			 VALUES ($1, $2, 'app', $3, 'main', $4, $5, $5)`,
			repoID, envID, sha, status, createdAt,
		); err != nil {
			t.Fatalf("seed build %s: %v", sha, err)
		}
	}

	regressedApp := "regressed-" + suffix
	regressedRepo := seedRepo(regressedApp)
	seedBuild(regressedRepo, "sha1", "success", time.Now().Add(-2*time.Hour))
	seedBuild(regressedRepo, "sha2", "failed", time.Now().Add(-1*time.Hour))

	healthyApp := "healthy-" + suffix
	healthyRepo := seedRepo(healthyApp)
	seedBuild(healthyRepo, "sha1", "failed", time.Now().Add(-2*time.Hour))
	seedBuild(healthyRepo, "sha2", "success", time.Now().Add(-1*time.Hour))

	out, err := h.overviewFailedLatestBuilds(context.Background())
	if err != nil {
		t.Fatalf("overviewFailedLatestBuilds: %v", err)
	}

	byApp := map[string]overviewFailedBuild{}
	for _, b := range out {
		byApp[b.AppName] = b
	}
	if b, ok := byApp[regressedApp]; !ok {
		t.Fatal("expected the app whose latest build failed to be reported")
	} else if b.CommitSha != "sha2" {
		t.Fatalf("CommitSha = %q, want the LATEST build's sha (sha2)", b.CommitSha)
	}
	if _, ok := byApp[healthyApp]; ok {
		t.Fatal("an app whose latest build succeeded must not be reported, even though an earlier build failed")
	}
}
