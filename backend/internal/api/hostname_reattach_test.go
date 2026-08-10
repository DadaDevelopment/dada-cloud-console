package api

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/config"
)

// seedReattachProjectEnv creates a throwaway project + k8s prod environment for
// the orphaned-hostname reattach tests. Cleanup cascades from the project.
func seedReattachProjectEnv(t *testing.T, pool *pgxpool.Pool) (projectID, envID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"reattach-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, projectID) })

	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type, runtime) VALUES ($1, 'prod', $2, 'prod', 'k8s') RETURNING id`,
		projectID, "reattach-ns-"+suffix,
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	return projectID, envID
}

// seedReattachApp inserts a live App resource_snapshot, synced well past
// defaultDomainBackfillGrace so ReattachOrphanedHostnames does not skip it as
// mid-flight.
func seedReattachApp(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName, summaryJSON string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, 'App', $3, 'Ready', $4::jsonb, now() - interval '1 hour')`,
		projectID, envID, appName, summaryJSON,
	); err != nil {
		t.Fatalf("seed resource_snapshots: %v", err)
	}
}

// seedFailedHostname inserts a domain_hostnames row already in 'failed', the
// state DeleteApp+CreateApp under the same name leaves an orphaned domain in.
// createdAt is backdated a month so a bug that forgets to reset the attach
// clock would be caught by TestReattachOrphanedHostnamesResetsAttachClock.
func seedFailedHostname(t *testing.T, pool *pgxpool.Pool, envID uuid.UUID, appName, hostname string, managed bool, authID *uuid.UUID, reattachCount int, updatedAgo time.Duration) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	recordType := "CNAME"
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO domain_hostnames (authorization_id, environment_id, app_name, hostname, record_type, status, cert_status, managed, created_at, attach_started_at, reattach_count)
		 VALUES ($1, $2, $3, $4, $5, 'failed', 'failed', $6, now() - interval '30 days', now() - interval '30 days', $7)
		 RETURNING id`,
		authID, envID, appName, hostname, recordType, managed, reattachCount,
	).Scan(&id); err != nil {
		t.Fatalf("seed failed hostname: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE domain_hostnames SET updated_at = now() - ($2 * INTERVAL '1 second') WHERE id = $1`,
		id, updatedAgo.Seconds(),
	); err != nil {
		t.Fatalf("backdate updated_at: %v", err)
	}
	return id
}

func seedVerifiedAuthorization(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID, userID uuid.UUID, apex string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO domain_authorizations (project_id, apex_domain, verification_token, status, verified_at, created_by)
		 VALUES ($1, $2, $3, 'verified', now(), $4)
		 RETURNING id`,
		projectID, apex, "tok-"+uuid.NewString()[:8], userID,
	).Scan(&id); err != nil {
		t.Fatalf("seed verified authorization: %v", err)
	}
	return id
}

func reattachTestConfig() *config.Config {
	return &config.Config{DefaultDomainEnabled: true, DefaultDomainBase: "dada-tuda.ru"}
}

func readHostnameRow(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (status string, reattachCount int, attachStartedAt time.Time, opID *uuid.UUID) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT status, reattach_count, attach_started_at, operation_id FROM domain_hostnames WHERE id = $1`, id,
	).Scan(&status, &reattachCount, &attachStartedAt, &opID); err != nil {
		t.Fatalf("read back hostname: %v", err)
	}
	return
}

// TestReattachOrphanedHostnamesReattachesManagedRow is the core positive case:
// a failed surrogate hostname whose app is alive and HTTP-serving is re-driven
// back to pending with an AttachDefaultDomain operation, its attach clock
// reset, and reattach_count bumped to 1.
func TestReattachOrphanedHostnamesReattachesManagedRow(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping reattach DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	appName := "magic-mirror-" + uuid.NewString()[:8]
	hostname := appName + "-ab12.dada-tuda.ru"
	seedReattachApp(t, pool, projectID, envID, appName, `{"port":8080}`)
	hostnameID := seedFailedHostname(t, pool, envID, appName, hostname, true, nil, 0, 24*time.Hour)

	if err := ReattachOrphanedHostnames(ctx, pool, reattachTestConfig()); err != nil {
		t.Fatalf("reattach: %v", err)
	}

	status, reattachCount, attachStartedAt, opID := readHostnameRow(t, pool, hostnameID)
	if status != "pending" {
		t.Fatalf("status = %q, want pending (live HTTP app must be re-driven)", status)
	}
	if reattachCount != 1 {
		t.Fatalf("reattach_count = %d, want 1", reattachCount)
	}
	if time.Since(attachStartedAt) > time.Minute {
		t.Fatalf("attach_started_at = %v, want freshly reset to now (else hostnamePendingExpired fires on the next tick)", attachStartedAt)
	}
	if opID == nil {
		t.Fatalf("operation_id is nil, want a new AttachDefaultDomain operation")
	}
	var action string
	if err := pool.QueryRow(ctx, `SELECT action FROM operations WHERE id = $1`, *opID).Scan(&action); err != nil {
		t.Fatalf("read operation: %v", err)
	}
	if action != "AttachDefaultDomain" {
		t.Errorf("operation action = %q, want AttachDefaultDomain for a managed row", action)
	}
}

// TestReattachOrphanedHostnamesSkipsDeadTail: a failed row whose app has no
// live App snapshot at all (the app itself was deleted or renamed, not just
// its Ingress) is a genuine dead tail and must never be touched -- there is
// nothing live to route to.
func TestReattachOrphanedHostnamesSkipsDeadTail(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping reattach DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	_, envID := seedReattachProjectEnv(t, pool)
	appName := "ghost-" + uuid.NewString()[:8]
	hostname := appName + "-ab12.dada-tuda.ru"
	hostnameID := seedFailedHostname(t, pool, envID, appName, hostname, true, nil, 0, 24*time.Hour)

	if err := ReattachOrphanedHostnames(ctx, pool, reattachTestConfig()); err != nil {
		t.Fatalf("reattach: %v", err)
	}

	status, reattachCount, _, opID := readHostnameRow(t, pool, hostnameID)
	if status != "failed" {
		t.Fatalf("status = %q, want failed (dead tail must not be touched)", status)
	}
	if reattachCount != 0 {
		t.Fatalf("reattach_count = %d, want 0", reattachCount)
	}
	if opID != nil {
		t.Fatalf("operation_id = %v, want nil (no operation should be enqueued)", *opID)
	}
}

// TestReattachOrphanedHostnamesSkipsWorkerApp: the app snapshot exists but was
// retrofitted as a worker (or lost its port), so appNeedsDefaultDomain is
// false -- re-driving would only re-render a route that 502s forever.
func TestReattachOrphanedHostnamesSkipsWorkerApp(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping reattach DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)

	t.Run("worker=true", func(t *testing.T) {
		appName := "wrk-" + uuid.NewString()[:8]
		hostname := appName + "-ab12.dada-tuda.ru"
		seedReattachApp(t, pool, projectID, envID, appName, `{"port":8080,"worker":true}`)
		hostnameID := seedFailedHostname(t, pool, envID, appName, hostname, true, nil, 0, 24*time.Hour)

		if err := ReattachOrphanedHostnames(ctx, pool, reattachTestConfig()); err != nil {
			t.Fatalf("reattach: %v", err)
		}
		status, _, _, _ := readHostnameRow(t, pool, hostnameID)
		if status != "failed" {
			t.Fatalf("status = %q, want failed (worker app has no HTTP route to restore)", status)
		}
	})

	t.Run("port=0", func(t *testing.T) {
		appName := "zero-" + uuid.NewString()[:8]
		hostname := appName + "-ab12.dada-tuda.ru"
		seedReattachApp(t, pool, projectID, envID, appName, `{"port":0}`)
		hostnameID := seedFailedHostname(t, pool, envID, appName, hostname, true, nil, 0, 24*time.Hour)

		if err := ReattachOrphanedHostnames(ctx, pool, reattachTestConfig()); err != nil {
			t.Fatalf("reattach: %v", err)
		}
		status, _, _, _ := readHostnameRow(t, pool, hostnameID)
		if status != "failed" {
			t.Fatalf("status = %q, want failed (portless app has no HTTP route to restore)", status)
		}
	})
}

// TestReattachOrphanedHostnamesSkipsUnverifiedCustomDomain: an unmanaged
// (custom-domain) row with no authorization, or one that is not verified,
// must never be silently re-attached -- that would (re-)issue a certificate
// for a hostname nobody has proven ownership of at this moment.
func TestReattachOrphanedHostnamesSkipsUnverifiedCustomDomain(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping reattach DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	appName := "web-" + uuid.NewString()[:8]
	hostname := "custom-" + uuid.NewString()[:8] + ".invalid"
	seedReattachApp(t, pool, projectID, envID, appName, `{"port":8080}`)
	hostnameID := seedFailedHostname(t, pool, envID, appName, hostname, false, nil, 0, 24*time.Hour)

	if err := ReattachOrphanedHostnames(ctx, pool, reattachTestConfig()); err != nil {
		t.Fatalf("reattach: %v", err)
	}

	status, reattachCount, _, opID := readHostnameRow(t, pool, hostnameID)
	if status != "failed" {
		t.Fatalf("status = %q, want failed (no verified authorization to trust)", status)
	}
	if reattachCount != 0 {
		t.Fatalf("reattach_count = %d, want 0", reattachCount)
	}
	if opID != nil {
		t.Fatalf("operation_id = %v, want nil", *opID)
	}
}

// TestReattachOrphanedHostnamesReattachesVerifiedCustomDomain is the ggrk52.ru
// shape from the incident: an unmanaged row backed by a still-verified
// authorization must be re-attached via AttachCustomHostname, keeping the
// same hostname and authorization_id (never recreated).
func TestReattachOrphanedHostnamesReattachesVerifiedCustomDomain(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping reattach DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	userID := seedUser(t, pool)
	appName := "magic-mirror-" + uuid.NewString()[:8]
	apex := "ggrk52-" + uuid.NewString()[:8] + ".invalid"
	authID := seedVerifiedAuthorization(t, pool, projectID, userID, apex)
	seedReattachApp(t, pool, projectID, envID, appName, `{"port":8080}`)
	hostnameID := seedFailedHostname(t, pool, envID, appName, apex, false, &authID, 0, 24*time.Hour)

	if err := ReattachOrphanedHostnames(ctx, pool, reattachTestConfig()); err != nil {
		t.Fatalf("reattach: %v", err)
	}

	status, reattachCount, _, opID := readHostnameRow(t, pool, hostnameID)
	if status != "pending" {
		t.Fatalf("status = %q, want pending", status)
	}
	if reattachCount != 1 {
		t.Fatalf("reattach_count = %d, want 1", reattachCount)
	}
	if opID == nil {
		t.Fatalf("operation_id is nil, want a new AttachCustomHostname operation")
	}
	var action string
	var storedHostname string
	var storedAuthID *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT action FROM operations WHERE id = $1`, *opID).Scan(&action); err != nil {
		t.Fatalf("read operation: %v", err)
	}
	if action != "AttachCustomHostname" {
		t.Errorf("operation action = %q, want AttachCustomHostname for an unmanaged row", action)
	}
	if err := pool.QueryRow(ctx, `SELECT hostname, authorization_id FROM domain_hostnames WHERE id = $1`, hostnameID).
		Scan(&storedHostname, &storedAuthID); err != nil {
		t.Fatalf("read back row: %v", err)
	}
	if storedHostname != apex {
		t.Errorf("hostname changed: got %q, want unchanged %q", storedHostname, apex)
	}
	if storedAuthID == nil || *storedAuthID != authID {
		t.Errorf("authorization_id changed or lost: got %v, want %v", storedAuthID, authID)
	}
}

// TestReattachOrphanedHostnamesRespectsAttemptCeiling: a row already at the
// max reattach count must be left alone even though its app is alive and
// healthy -- the ceiling is a hard stop, not just a slower cadence, per the
// project's rule after the 56.5k-generation DNS storm.
func TestReattachOrphanedHostnamesRespectsAttemptCeiling(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping reattach DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	appName := "capped-" + uuid.NewString()[:8]
	hostname := appName + "-ab12.dada-tuda.ru"
	seedReattachApp(t, pool, projectID, envID, appName, `{"port":8080}`)
	hostnameID := seedFailedHostname(t, pool, envID, appName, hostname, true, nil, hostnameReattachMaxAttempts, 24*time.Hour)

	if err := ReattachOrphanedHostnames(ctx, pool, reattachTestConfig()); err != nil {
		t.Fatalf("reattach: %v", err)
	}

	status, reattachCount, _, _ := readHostnameRow(t, pool, hostnameID)
	if status != "failed" {
		t.Fatalf("status = %q, want failed (attempt ceiling must hold)", status)
	}
	if reattachCount != hostnameReattachMaxAttempts {
		t.Fatalf("reattach_count = %d, want unchanged %d", reattachCount, hostnameReattachMaxAttempts)
	}
}

// TestReattachOrphanedHostnamesRespectsCooldown: a row reattached (or simply
// touched) within the cooldown window must not be re-driven again on this
// tick, even though every other precondition is met.
func TestReattachOrphanedHostnamesRespectsCooldown(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping reattach DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	appName := "cooling-" + uuid.NewString()[:8]
	hostname := appName + "-ab12.dada-tuda.ru"
	seedReattachApp(t, pool, projectID, envID, appName, `{"port":8080}`)
	hostnameID := seedFailedHostname(t, pool, envID, appName, hostname, true, nil, 1, time.Hour)

	if err := ReattachOrphanedHostnames(ctx, pool, reattachTestConfig()); err != nil {
		t.Fatalf("reattach: %v", err)
	}

	status, reattachCount, _, _ := readHostnameRow(t, pool, hostnameID)
	if status != "failed" {
		t.Fatalf("status = %q, want failed (cooldown must hold off a re-drive from 1 hour ago)", status)
	}
	if reattachCount != 1 {
		t.Fatalf("reattach_count = %d, want unchanged 1", reattachCount)
	}
}

// TestReattachOrphanedHostnamesUnblocksReconcileWindow is the anti-storm
// guarantee this whole fix depends on: after a reattach resets
// attach_started_at, ReconcilePendingHostnames must NOT immediately fail the
// row again even though its original created_at is a month old. Without the
// reset this would be an infinite failed/pending flap.
func TestReattachOrphanedHostnamesUnblocksReconcileWindow(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping reattach DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	appName := "unstick-" + uuid.NewString()[:8]
	hostname := appName + "-ab12.dada-tuda.ru"
	seedReattachApp(t, pool, projectID, envID, appName, `{"port":8080}`)
	hostnameID := seedFailedHostname(t, pool, envID, appName, hostname, true, nil, 0, 24*time.Hour)

	if err := ReattachOrphanedHostnames(ctx, pool, reattachTestConfig()); err != nil {
		t.Fatalf("reattach: %v", err)
	}
	status, _, _, _ := readHostnameRow(t, pool, hostnameID)
	if status != "pending" {
		t.Fatalf("status after reattach = %q, want pending", status)
	}

	if err := ReconcilePendingHostnames(ctx, pool, unreachableProbeConfig()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	status, _, _, _ = readHostnameRow(t, pool, hostnameID)
	if status != "pending" {
		t.Fatalf("status after immediate reconcile tick = %q, want still pending (created_at is a month old; only the reset attach_started_at must be honored)", status)
	}
}
