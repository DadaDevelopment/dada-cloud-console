package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedAppSnapshotAged inserts one App resource_snapshots row with explicit
// first_seen_at and last_synced_at ages, so a test can reproduce the exact
// "fresh last_synced_at, stale first_seen_at" shape a reconcile tick leaves
// behind on a long-neglected app.
func seedAppSnapshotAged(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, name, phase, liveSource, firstSeenAge, lastSyncedAge string) {
	t.Helper()
	summary := `{}`
	if liveSource != "" {
		summary = `{"live_source":"` + liveSource + `"}`
	}
	_, err := pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, first_seen_at, last_synced_at)
		 VALUES ($1, $2, 'App', $3, $4, $5::jsonb, now() - $6::interval, now() - $7::interval)`,
		projectID, envID, name, phase, summary, firstSeenAge, lastSyncedAge,
	)
	if err != nil {
		t.Fatalf("seed app snapshot %s: %v", name, err)
	}
}

// TestFetchAppSnapshot_RoundTripsRowFieldsClassifyAppHealthNeeds is the
// real-DB proof that fetchAppSnapshot's query wires phase/live_source/
// first_seen_at/last_synced_at correctly into classifyAppHealth's inputs --
// the pure classifyAppHealth tests in app_health_test.go assume this wiring,
// this test proves it against real Postgres rather than a mock.
func TestFetchAppSnapshot_RoundTripsRowFieldsClassifyAppHealthNeeds(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	projectID := overviewBrokenSeedProject(t, pool, "apphealth-fetch-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	seedAppSnapshotAged(t, pool, projectID, envID, "crashing-"+suffix, "CrashLoopBackOff", "k8s", "2 days", "1 minute")

	row, found, err := h.fetchAppSnapshot(context.Background(), projectID, envID, "crashing-"+suffix)
	if err != nil {
		t.Fatalf("fetchAppSnapshot: %v", err)
	}
	if !found {
		t.Fatal("expected the seeded snapshot to be found")
	}
	if row.Phase != "CrashLoopBackOff" {
		t.Errorf("Phase = %q, want CrashLoopBackOff", row.Phase)
	}
	if row.LiveSource != "k8s" {
		t.Errorf("LiveSource = %q, want k8s", row.LiveSource)
	}
	if row.FirstSeenAt.After(time.Now().Add(-23 * time.Hour)) {
		t.Errorf("FirstSeenAt = %v, want ~2 days ago", row.FirstSeenAt)
	}
	if row.LastSyncedAt.Before(time.Now().Add(-5 * time.Minute)) {
		t.Errorf("LastSyncedAt = %v, want ~1 minute ago", row.LastSyncedAt)
	}

	verdict, stale := classifyAppHealth(row)
	if verdict != appHealthNotReady || stale {
		t.Errorf("classifyAppHealth(row) = (%q, %v), want (%q, false)", verdict, stale, appHealthNotReady)
	}
}

// TestFetchAppSnapshot_NoRowMeansNotFound proves an app with no
// resource_snapshots row (e.g. an upload-deploy app whose first build never
// produced a workload) comes back found=false rather than an error, which is
// what lets GetAppHealth answer "unknown" instead of 500ing.
func TestFetchAppSnapshot_NoRowMeansNotFound(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	projectID := overviewBrokenSeedProject(t, pool, "apphealth-missing-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	_, found, err := h.fetchAppSnapshot(context.Background(), projectID, envID, "never-existed-"+suffix)
	if err != nil {
		t.Fatalf("fetchAppSnapshot: %v", err)
	}
	if found {
		t.Fatal("expected found=false for an app with no snapshot row")
	}
}

// TestFetchAppSnapshot_NoSignalGrabliAgainstRealPostgres re-runs the
// last_synced_at-vs-first_seen_at grabli end to end through the real query:
// a row whose last_synced_at was just re-stamped by an unrelated reconcile
// tick but whose first_seen_at is days old must still classify as no_signal,
// not as young/unknown.
func TestFetchAppSnapshot_NoSignalGrabliAgainstRealPostgres(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	projectID := overviewBrokenSeedProject(t, pool, "apphealth-grabli-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	seedAppSnapshotAged(t, pool, projectID, envID, "frozen-"+suffix, "Pending", "", "3 days", "0 seconds")

	row, found, err := h.fetchAppSnapshot(context.Background(), projectID, envID, "frozen-"+suffix)
	if err != nil {
		t.Fatalf("fetchAppSnapshot: %v", err)
	}
	if !found {
		t.Fatal("expected the seeded snapshot to be found")
	}

	verdict, _ := classifyAppHealth(row)
	if verdict != appHealthNoSignal {
		t.Fatalf("verdict = %q, want %q: first_seen_at is 3 days old even though last_synced_at is fresh", verdict, appHealthNoSignal)
	}
}
