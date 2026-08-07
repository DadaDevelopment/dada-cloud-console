package api

import "testing"

// Placement must land a new database on the emptiest open shard. This is the
// whole point of the registry: without it every database keeps piling onto the
// one instance that already carries the platform's data.
func TestPickTenantShard_PicksEmptiest(t *testing.T) {
	shards := []dbShard{
		{Name: "shard-1", State: dbShardStateOpen},
		{Name: "shard-2", State: dbShardStateOpen},
	}
	used := map[string]int64{"shard-1": 20 << 30, "shard-2": 3 << 30}
	if got := pickTenantShard(shards, used); got != "shard-2" {
		t.Errorf("placement ignored fill level: got %q, want shard-2", got)
	}
}

// A shard that has reached its declared capacity is skipped even when it is
// still the emptiest by absolute bytes — capacity, not size, is what says "this
// instance is done taking tenants".
func TestPickTenantShard_SkipsFullShard(t *testing.T) {
	shards := []dbShard{
		{Name: "shard-1", State: dbShardStateOpen, CapacityBytes: 4 << 30},
		{Name: "shard-2", State: dbShardStateOpen},
	}
	used := map[string]int64{"shard-1": 5 << 30, "shard-2": 9 << 30}
	if got := pickTenantShard(shards, used); got != "shard-2" {
		t.Errorf("placed onto a shard past its capacity: got %q, want shard-2", got)
	}
}

// An unmeasured shard counts as empty, not as full: a freshly added shard has
// no pg_database_size_bytes series yet, and a broken exporter must not freeze
// placement onto the instance that still reports.
func TestPickTenantShard_UnmeasuredCountsEmpty(t *testing.T) {
	shards := []dbShard{
		{Name: "shard-1", State: dbShardStateOpen},
		{Name: "shard-9", State: dbShardStateOpen},
	}
	used := map[string]int64{"shard-1": 1 << 30}
	if got := pickTenantShard(shards, used); got != "shard-9" {
		t.Errorf("unmeasured shard was treated as full: got %q, want shard-9", got)
	}
}

// With nothing measured at all, placement still has to be reproducible: two
// replicas deciding at the same moment must not disagree about where a
// database goes.
func TestPickTenantShard_TieIsDeterministic(t *testing.T) {
	shards := []dbShard{
		{Name: "shard-2", State: dbShardStateOpen},
		{Name: "shard-1", State: dbShardStateOpen},
	}
	for i := 0; i < 5; i++ {
		if got := pickTenantShard(shards, nil); got != "shard-1" {
			t.Fatalf("tie-break not deterministic: got %q, want shard-1", got)
		}
	}
}

// No qualifying shard yields "", which omits spec.shard and leaves the XRD
// default in charge — the placement every database has today. Failing open
// here is deliberate: a registry problem must not stop a tenant from creating
// a database.
func TestPickTenantShard_NoCandidatesFallsBackToDefault(t *testing.T) {
	if got := pickTenantShard(nil, nil); got != "" {
		t.Errorf("expected empty fallback, got %q", got)
	}
	full := []dbShard{{Name: "shard-1", State: dbShardStateOpen, CapacityBytes: 1 << 30}}
	if got := pickTenantShard(full, map[string]int64{"shard-1": 2 << 30}); got != "" {
		t.Errorf("expected empty fallback when every shard is full, got %q", got)
	}
}
