package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedNoSignalApp inserts an App snapshot with an explicit age, so a test can
// state what the platform knew and since when. liveSource is written as a JSON
// null when empty, which is the shape a snapshot has before any reconciler has
// observed a workload for it.
func seedNoSignalApp(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, name, phase, liveSource, age string) {
	t.Helper()
	summary := `{}`
	if liveSource != "" {
		summary = `{"live_source":"` + liveSource + `"}`
	}
	_, err := pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, first_seen_at, last_synced_at)
		 VALUES ($1, $2, 'App', $3, $4, $5::jsonb, now() - $6::interval, now())`,
		projectID, envID, name, phase, summary, age,
	)
	if err != nil {
		t.Fatalf("seed app snapshot %s: %v", name, err)
	}
}

// TestOverviewNoSignalAppsSurfacesTheBlindSpot pins the gap that let
// apps.broken report 0 while nine App rows sat in by_phase.Unknown:
// brokenAppSnapshotPredicate can only indict a live k8s workload, so an app
// with no workload at all was counted as neither ready nor broken and vanished
// from the panel entirely. Such a row must now be named, and it must NOT leak
// into the not-ready list, which is deliberately about proven breakage.
func TestOverviewNoSignalAppsSurfacesTheBlindSpot(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	projectID := overviewBrokenSeedProject(t, pool, "nosignal-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	seedNoSignalApp(t, pool, projectID, envID, "never-deployed-"+suffix, "", "", "3 days")
	seedNoSignalApp(t, pool, projectID, envID, "frozen-pending-"+suffix, "Pending", "", "2 days")

	out, err := h.overviewNoSignalApps(context.Background())
	if err != nil {
		t.Fatalf("overviewNoSignalApps: %v", err)
	}
	byName := map[string]overviewNoSignalApp{}
	for _, a := range out {
		byName[a.Name] = a
	}

	got, ok := byName["never-deployed-"+suffix]
	if !ok {
		t.Fatal("an App snapshot with no live_source must be reported as having no health signal; this is the row class that made apps.broken read 0 while nothing was known about it")
	}
	if got.Phase != "Unknown" {
		t.Fatalf("Phase = %q, want %q: an empty phase is exactly the absent answer this bucket exists to name", got.Phase, "Unknown")
	}
	if got.AgeSeconds < 2*24*3600 {
		t.Fatalf("AgeSeconds = %d, want the age since first_seen_at (~3 days)", got.AgeSeconds)
	}
	if _, ok := byName["frozen-pending-"+suffix]; !ok {
		t.Fatal("a Pending snapshot frozen at git-watcher create time has no workload either and must be reported")
	}

	notReady, err := h.overviewNotReadyApps(context.Background())
	if err != nil {
		t.Fatalf("overviewNotReadyApps: %v", err)
	}
	for _, a := range notReady {
		if a.Name == "never-deployed-"+suffix || a.Name == "frozen-pending-"+suffix {
			t.Fatalf("%s has no live workload, so it cannot be PROVEN broken; the not-ready list must stay about proven breakage", a.Name)
		}
	}
}

// TestOverviewNoSignalAppsExcludesSettledAndYoung is the control case. Every
// exclusion here is a way the bucket could cry wolf: a live workload has a
// signal, Ready/Stopped/Orphaned are settled answers rather than missing ones,
// and an app created minutes ago has no workload for the perfectly ordinary
// reason that its first build is still running.
func TestOverviewNoSignalAppsExcludesSettledAndYoung(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	projectID := overviewBrokenSeedProject(t, pool, "nosignalctl-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	seedNoSignalApp(t, pool, projectID, envID, "live-"+suffix, "CrashLoopBackOff", "k8s", "3 days")
	seedNoSignalApp(t, pool, projectID, envID, "stopped-"+suffix, "Stopped", "", "3 days")
	seedNoSignalApp(t, pool, projectID, envID, "orphaned-"+suffix, "Orphaned", "", "3 days")
	seedNoSignalApp(t, pool, projectID, envID, "ready-"+suffix, "Ready", "", "3 days")
	seedNoSignalApp(t, pool, projectID, envID, "just-created-"+suffix, "Pending", "", "5 minutes")

	out, err := h.overviewNoSignalApps(context.Background())
	if err != nil {
		t.Fatalf("overviewNoSignalApps: %v", err)
	}
	for _, a := range out {
		switch a.Name {
		case "live-" + suffix:
			t.Fatal("a k8s workload HAS a health signal; it belongs in the not-ready list, not here")
		case "stopped-" + suffix:
			t.Fatal("Stopped is a deliberate answer, not a missing one")
		case "orphaned-" + suffix:
			t.Fatal("Orphaned is a settled answer, not a missing one")
		case "ready-" + suffix:
			t.Fatal("Ready is a settled answer, not a missing one")
		case "just-created-" + suffix:
			t.Fatal("an app created 5 minutes ago is mid-first-build; the grace window exists so the panel does not report normal provisioning as a problem")
		}
	}
}

// TestOverviewNoSignalGraceKeysOnFirstSeen guards the specific mistake this
// grace window has already caused elsewhere in the codebase: keying it on
// last_synced_at instead of first_seen_at. last_synced_at is re-stamped on
// every reconcile tick, so an app that has been signal-less for days would
// keep looking brand new and never surface.
func TestOverviewNoSignalGraceKeysOnFirstSeen(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	projectID := overviewBrokenSeedProject(t, pool, "nosignalgrace-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	name := "old-but-freshly-synced-" + suffix
	seedNoSignalApp(t, pool, projectID, envID, name, "Pending", "", "9 days")

	out, err := h.overviewNoSignalApps(context.Background())
	if err != nil {
		t.Fatalf("overviewNoSignalApps: %v", err)
	}
	for _, a := range out {
		if a.Name == name {
			return
		}
	}
	t.Fatal("a 9-day-old signal-less app whose snapshot was re-synced seconds ago must still be reported: the grace window has to read first_seen_at, not last_synced_at")
}

// TestOverviewProjectsCountsNoSignal ties the headline number to the list, the
// same way brokenAppSnapshotPredicate is shared between apps.broken and the
// not-ready list so the two can never disagree.
func TestOverviewProjectsCountsNoSignal(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	projectID := overviewBrokenSeedProject(t, pool, "nosignalcount-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")
	seedNoSignalApp(t, pool, projectID, envID, "counted-"+suffix, "Pending", "", "4 days")

	projects, err := h.overviewProjects(context.Background())
	if err != nil {
		t.Fatalf("overviewProjects: %v", err)
	}
	list, err := h.overviewNoSignalApps(context.Background())
	if err != nil {
		t.Fatalf("overviewNoSignalApps: %v", err)
	}
	if projects.Apps.NoSignal < 1 {
		t.Fatalf("Apps.NoSignal = %d, want at least the one seeded signal-less app", projects.Apps.NoSignal)
	}
	if len(list) < 100 && projects.Apps.NoSignal != len(list) {
		t.Fatalf("Apps.NoSignal = %d but the list holds %d rows; the count and the list must come from one predicate", projects.Apps.NoSignal, len(list))
	}
}
