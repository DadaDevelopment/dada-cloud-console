package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestVolumeInodesExhaustedReadsRatioKindWrittenByTouch is the real-DB proof
// that the crash watcher's inode lookup (volumeInodesExhausted) actually sees
// what the volume watcher's own tick wrote, round-tripped through Postgres
// rather than asserted in memory. This is the wiring the fonbet-value fix
// depends on end to end: touchAppVolumeAlertSeen records "still observed hot
// on inodes right now" on every 15-minute tick regardless of whether an email
// was sent, and volumeInodesExhausted is the crash watcher's only way to
// learn that fact without firing a second, redundant Prometheus query.
func TestVolumeInodesExhaustedReadsRatioKindWrittenByTouch(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]
	namespace := "vol-inode-test-" + suffix
	appName := "fonbet-value-" + suffix

	if h.volumeInodesExhausted(context.Background(), namespace, appName) {
		t.Fatal("expected no rows yet, so volumeInodesExhausted must report false")
	}

	touchAppVolumeAlertSeen(context.Background(), pool, namespace, appName, 1.0, ratioKindInodes)

	if !h.volumeInodesExhausted(context.Background(), namespace, appName) {
		t.Fatal("expected volumeInodesExhausted to read back the inodes ratio_kind touch just wrote")
	}
}

// TestVolumeInodesExhaustedFalseForBytesOnlyHit is the negative case: a PVC
// that is only byte-hot (the ordinary, pre-existing alert shape) must not
// make the crash watcher believe it saw inode exhaustion.
func TestVolumeInodesExhaustedFalseForBytesOnlyHit(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]
	namespace := "vol-bytes-test-" + suffix
	appName := "byte-heavy-app-" + suffix

	touchAppVolumeAlertSeen(context.Background(), pool, namespace, appName, 0.9, ratioKindBytes)

	if h.volumeInodesExhausted(context.Background(), namespace, appName) {
		t.Fatal("expected a bytes-tagged touch to leave volumeInodesExhausted false")
	}
}

// TestClaimAppVolumeAlertSlotRoundTripsRatioKind proves claimAppVolumeAlertSlot
// (the cooldown-gated path that actually sends the owner email) persists the
// ratio_kind it is given, using one row per (namespace, app_name) as before --
// the migration must not have multiplied the primary key or dropped the
// column write silently.
func TestClaimAppVolumeAlertSlotRoundTripsRatioKind(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	suffix := uuid.NewString()[:8]
	namespace := "vol-claim-test-" + suffix
	appName := "claim-app-" + suffix

	claimed := claimAppVolumeAlertSlot(context.Background(), pool, namespace, appName, 0.99, ratioKindInodes, 24*time.Hour)
	if !claimed {
		t.Fatal("expected first claim on a fresh (namespace, app_name) to succeed")
	}

	var kind string
	var ratio float64
	if err := pool.QueryRow(context.Background(),
		`SELECT ratio, ratio_kind FROM app_volume_alerts WHERE namespace = $1 AND app_name = $2`,
		namespace, appName,
	).Scan(&ratio, &kind); err != nil {
		t.Fatalf("read back claimed row: %v", err)
	}
	if kind != ratioKindInodes || ratio != 0.99 {
		t.Fatalf("expected ratio=0.99 ratio_kind=inodes, got ratio=%v ratio_kind=%q", ratio, kind)
	}

	if claimAppVolumeAlertSlot(context.Background(), pool, namespace, appName, 0.5, ratioKindBytes, 24*time.Hour) {
		t.Fatal("expected the second claim within the cooldown window to be refused")
	}
}
