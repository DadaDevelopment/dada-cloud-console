package api

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedBackfillApp inserts an App snapshot with an explicit phase, first seen an
// hour ago and synced just now -- the shape of every healthy app on prod.
func seedBackfillApp(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName, phase, summaryJSON string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, first_seen_at, last_synced_at)
		 VALUES ($1, $2, 'App', $3, $4, $5::jsonb, now() - interval '1 hour', now())`,
		projectID, envID, appName, phase, summaryJSON,
	); err != nil {
		t.Fatalf("seed resource_snapshots: %v", err)
	}
}

func hostnameRowsForApp(t *testing.T, pool *pgxpool.Pool, envID uuid.UUID, appName string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM domain_hostnames WHERE environment_id = $1 AND app_name = $2`,
		envID, appName,
	).Scan(&n); err != nil {
		t.Fatalf("count hostnames: %v", err)
	}
	return n
}

// TestBackfillMissingDefaultDomainsMeasuresAgeByFirstSeen pins which column the
// grace window is measured against. last_synced_at is refreshed by every
// snapshot-sync tick, so gating on it excludes every live app and admits only
// stale ones -- the pass would then be structurally unable to fix the app it
// was written for. first_seen_at is the app's actual age.
func TestBackfillMissingDefaultDomainsMeasuresAgeByFirstSeen(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping backfill DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)

	oldApp := "backfill-old-" + uuid.NewString()[:8]
	seedBackfillApp(t, pool, projectID, envID, oldApp, "Ready", `{"port":8080}`)

	newApp := "backfill-new-" + uuid.NewString()[:8]
	seedFreshlyCreatedApp(t, pool, projectID, envID, newApp, `{"port":8080}`)

	if err := BackfillMissingDefaultDomains(ctx, pool, reattachTestConfig()); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if n := hostnameRowsForApp(t, pool, envID, oldApp); n != 1 {
		t.Fatalf("long-lived app: %d hostname rows, want 1 -- a constantly re-synced snapshot must not be read as mid-flight", n)
	}
	if n := hostnameRowsForApp(t, pool, envID, newApp); n != 0 {
		t.Fatalf("seconds-old app: %d hostname rows, want 0 -- CreateApp's own domain step may still be running", n)
	}
}

// TestBackfillMissingDefaultDomainsSkipsOrphanedSnapshot: an Orphaned snapshot
// is a deleted app awaiting purge, hidden from every other reader
// (notOrphanedSnapshot). Provisioning a public hostname and a certificate for
// one would resurrect a dead app as a live address.
func TestBackfillMissingDefaultDomainsSkipsOrphanedSnapshot(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping backfill DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	deadApp := "backfill-orphan-" + uuid.NewString()[:8]
	seedBackfillApp(t, pool, projectID, envID, deadApp, "Orphaned", `{"port":8080}`)

	if err := BackfillMissingDefaultDomains(ctx, pool, reattachTestConfig()); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if n := hostnameRowsForApp(t, pool, envID, deadApp); n != 0 {
		t.Fatalf("orphaned app: %d hostname rows, want 0 -- a deleted app must not get a public address", n)
	}
}

// TestBackfillMissingDefaultDomainsSkipsPortlessSnapshot guards the blast
// radius of the first_seen_at fix: 71 of prod's 81 App snapshots carry no port
// (platform/infra apps synced from hand-maintained gitops trees), and they must
// stay excluded, otherwise widening the age gate would mint surrogate hostnames
// and certificates for dozens of internal workloads at once.
func TestBackfillMissingDefaultDomainsSkipsPortlessSnapshot(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping backfill DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)
	infraApp := "backfill-infra-" + uuid.NewString()[:8]
	seedBackfillApp(t, pool, projectID, envID, infraApp, "Ready", `{"status":"Ready","message":"Synced from git"}`)

	if err := BackfillMissingDefaultDomains(ctx, pool, reattachTestConfig()); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if n := hostnameRowsForApp(t, pool, envID, infraApp); n != 0 {
		t.Fatalf("portless infra app: %d hostname rows, want 0", n)
	}
}

// TestBackfillMissingDefaultDomainsSkipsAdoptedApp pins the incident of
// 2026-08-22: adopt-config taught the console the ports of 53 hand-maintained
// gitops apps, and this backfill read those ports as "an app the console
// publishes", minting public hostnames plus certificates for three
// internal-only services (reels-task-tools, telemost-task-tools, ai-gateway).
// A port learned from git is a report about the app, never a request to put it
// on the internet.
func TestBackfillMissingDefaultDomainsSkipsAdoptedApp(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping backfill DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	projectID, envID := seedReattachProjectEnv(t, pool)

	adopted := "backfill-adopted-" + uuid.NewString()[:8]
	seedBackfillApp(t, pool, projectID, envID, adopted, "Ready", `{"port":8080,"port_source":"adopted"}`)

	console := "backfill-console-" + uuid.NewString()[:8]
	seedBackfillApp(t, pool, projectID, envID, console, "Ready", `{"port":8080,"port_source":"user"}`)

	if err := BackfillMissingDefaultDomains(ctx, pool, reattachTestConfig()); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if n := hostnameRowsForApp(t, pool, envID, adopted); n != 0 {
		t.Fatalf("adopted app: %d hostname rows, want 0 -- adoption reads git, it does not ask to publish the app", n)
	}
	if n := hostnameRowsForApp(t, pool, envID, console); n != 1 {
		t.Fatalf("console-created app: %d hostname rows, want 1 -- the backfill must still fix its own missing row", n)
	}
}
