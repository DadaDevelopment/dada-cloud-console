package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// overviewKindLagSeed inserts a resource_snapshots row with an explicit
// last_synced_at so tests can control exactly how far a row lags behind its
// kind's newest sync, the input KindLagSeconds is computed from.
func overviewKindLagSeed(t *testing.T, h *Handler, projectID, envID uuid.UUID, kind, name, phase, liveSource string, syncedAt time.Time) {
	t.Helper()
	_, err := h.pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, $3, $4, $5, jsonb_build_object('live_source', $6::text), $7)`,
		projectID, envID, kind, name, phase, liveSource, syncedAt,
	)
	if err != nil {
		t.Fatalf("seed %s snapshot %s: %v", kind, name, err)
	}
}

// TestOverviewNotReadyOtherResourcesFreshRowNotUnmaintained covers the case
// where a not-ready row's last_synced_at equals its kind's newest sync: the
// reconciler visited it in the very same pass, so it must read as maintained
// (Unmaintained=false, KindLagSeconds ~0) while still appearing in the list
// because it is genuinely not-ready.
func TestOverviewNotReadyOtherResourcesFreshRowNotUnmaintained(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	projectID := overviewBrokenSeedProject(t, pool, "kindlag-fresh-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	now := time.Now()
	name := "publicapi-fresh-" + suffix
	overviewKindLagSeed(t, h, projectID, envID, "PublicApi", name, "Pending", "crd", now)

	out, err := h.overviewNotReadyOtherResources(context.Background())
	if err != nil {
		t.Fatalf("overviewNotReadyOtherResources: %v", err)
	}

	var found *overviewNotReadyResource
	for i := range out {
		if out[i].Name == name {
			found = &out[i]
		}
	}
	if found == nil {
		t.Fatalf("expected %s in the not-ready list, it is still Pending", name)
	}
	if found.Unmaintained {
		t.Fatalf("Unmaintained = true, want false: this row's last_synced_at IS its kind's newest sync, lag = %d", found.KindLagSeconds)
	}
	if found.KindLagSeconds > 5 {
		t.Fatalf("KindLagSeconds = %d, want ~0: this row is the newest of its kind+live_source", found.KindLagSeconds)
	}
}

// TestOverviewNotReadyOtherResourcesStaleRowFlaggedNotFiltered is the
// regression guard: a not-ready row abandoned by the reconciler (a fresh
// sibling of the same kind+live_source proves the writer is still running)
// must be flagged Unmaintained=true but MUST still appear in the returned
// list. Three PublicApi rows sat unflagged-but-listed as "broken" for 7-20
// days in production; filtering them out here would have been the wrong fix
// just as much as leaving them unflagged was the original bug.
func TestOverviewNotReadyOtherResourcesStaleRowFlaggedNotFiltered(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	projectID := overviewBrokenSeedProject(t, pool, "kindlag-stale-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	now := time.Now()
	staleName := "publicapi-stale-" + suffix
	freshName := "publicapi-sibling-" + suffix

	overviewKindLagSeed(t, h, projectID, envID, "PublicApi", staleName, "Pending", "crd", now.Add(-3*time.Hour))
	overviewKindLagSeed(t, h, projectID, envID, "PublicApi", freshName, "Ready", "crd", now)

	out, err := h.overviewNotReadyOtherResources(context.Background())
	if err != nil {
		t.Fatalf("overviewNotReadyOtherResources: %v", err)
	}

	var found *overviewNotReadyResource
	for i := range out {
		if out[i].Name == staleName {
			found = &out[i]
		}
		if out[i].Name == freshName {
			t.Fatalf("the Ready sibling must not appear in the not-ready list at all")
		}
	}
	if found == nil {
		t.Fatalf("expected %s in the not-ready list even though it is abandoned: Unmaintained rows are flagged, never filtered", staleName)
	}
	if !found.Unmaintained {
		t.Fatalf("Unmaintained = false, want true: lagging 3 hours behind a fresh sibling of the same kind+live_source, lag = %d", found.KindLagSeconds)
	}
	if found.KindLagSeconds <= otherSnapshotAbandonLagSeconds {
		t.Fatalf("KindLagSeconds = %d, want > %d", found.KindLagSeconds, otherSnapshotAbandonLagSeconds)
	}
}

// TestOverviewNotReadyOtherResourcesWholeKindAbandonedStaysAlarmed asserts
// the other half of the design: when every row of a kind+live_source is
// equally stale (no fresh sibling anywhere, i.e. the reconciler for that kind
// died entirely), the newest-of-kind is itself old, so every row's lag stays
// near zero relative to it and Unmaintained stays false. A dead reconciler
// must keep reading as breakage rather than being silently excused by its own
// blindness.
func TestOverviewNotReadyOtherResourcesWholeKindAbandonedStaysAlarmed(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	projectID := overviewBrokenSeedProject(t, pool, "kindlag-dead-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	now := time.Now()
	nameA := "kservemodel-a-" + suffix
	nameB := "kservemodel-b-" + suffix

	overviewKindLagSeed(t, h, projectID, envID, "KServeModel", nameA, "Pending", "kserve", now.Add(-5*time.Hour))
	overviewKindLagSeed(t, h, projectID, envID, "KServeModel", nameB, "Pending", "kserve", now.Add(-5*time.Hour).Add(-2*time.Second))

	out, err := h.overviewNotReadyOtherResources(context.Background())
	if err != nil {
		t.Fatalf("overviewNotReadyOtherResources: %v", err)
	}

	byName := map[string]overviewNotReadyResource{}
	for _, r := range out {
		byName[r.Name] = r
	}
	for _, name := range []string{nameA, nameB} {
		r, ok := byName[name]
		if !ok {
			t.Fatalf("expected %s in the not-ready list: a whole dead kind must still surface as breakage", name)
		}
		if r.Unmaintained {
			t.Fatalf("%s: Unmaintained = true, want false: no fresh sibling exists for this kind+live_source, so a dead reconciler must keep alarming, lag = %d", name, r.KindLagSeconds)
		}
	}
}

// TestOverviewNotReadyOtherResourcesLagPartitionedByKindAndLiveSource checks
// that the newest-sync partition is per (kind, live_source): a fresh row of a
// DIFFERENT kind must not make an unrelated kind's stale rows look any more
// or less abandoned than they already are.
func TestOverviewNotReadyOtherResourcesLagPartitionedByKindAndLiveSource(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	projectID := overviewBrokenSeedProject(t, pool, "kindlag-partition-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	now := time.Now()
	staleDBName := "svcdb-stale-" + suffix
	freshOtherKindName := "publicapi-other-" + suffix

	overviewKindLagSeed(t, h, projectID, envID, "ServiceDatabaseV2", staleDBName, "Pending", "crossplane", now.Add(-4*time.Hour))
	overviewKindLagSeed(t, h, projectID, envID, "PublicApi", freshOtherKindName, "Pending", "crd", now)

	out, err := h.overviewNotReadyOtherResources(context.Background())
	if err != nil {
		t.Fatalf("overviewNotReadyOtherResources: %v", err)
	}

	byName := map[string]overviewNotReadyResource{}
	for _, r := range out {
		byName[r.Name] = r
	}

	staleDB, ok := byName[staleDBName]
	if !ok {
		t.Fatalf("expected %s in the not-ready list", staleDBName)
	}
	if staleDB.Unmaintained {
		t.Fatalf("%s: Unmaintained = true, want false: it is the ONLY ServiceDatabaseV2/crossplane row, so it is its own kind's newest and must not be flagged just because a PublicApi row elsewhere is fresh, lag = %d", staleDBName, staleDB.KindLagSeconds)
	}
	if staleDB.KindLagSeconds > 5 {
		t.Fatalf("%s: KindLagSeconds = %d, want ~0: the fresh row belongs to a different kind and must not bleed into this partition", staleDBName, staleDB.KindLagSeconds)
	}

	freshOther, ok := byName[freshOtherKindName]
	if !ok {
		t.Fatalf("expected %s in the not-ready list", freshOtherKindName)
	}
	if freshOther.Unmaintained {
		t.Fatalf("%s: Unmaintained = true, want false, lag = %d", freshOtherKindName, freshOther.KindLagSeconds)
	}
}
