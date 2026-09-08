package api

import (
	"strings"
	"testing"
)

// TestIsManagedShardHost_ShardAddressesAreInternal pins the boundary the whole
// file exists for: which endpoints may be handed to a tenant. A shard address
// leaking into a DSN is what the repo's pre-commit hook and the
// db-dsn-no-shard-host admission policy both reject, and what survives a move
// as a live connection to the wrong instance.
func TestIsManagedShardHost_ShardAddressesAreInternal(t *testing.T) {
	internal := []string{
		"pg-shard-0-postgresql.databases.svc.cluster.local",
		"postgresql.databases.svc.cluster.local",
		"pg-shard-0-postgresql.databases.svc",
		"pg-shard-0.databases",
		"PG-SHARD-0-POSTGRESQL.DATABASES.SVC.CLUSTER.LOCAL",
		"pg-shard-0-postgresql.databases.svc.cluster.local.",
	}
	for _, host := range internal {
		if !isManagedShardHost(host) {
			t.Errorf("isManagedShardHost(%q) = false, want true (a shard address must never reach a tenant)", host)
		}
	}

	public := []string{
		"",
		pgRouterInternalHost,
		pgRouterTLSHostname,
		"pg-router.databases.svc",
		"pg-router-admin.databases.svc",
		"db-external.pv.dada-tuda.ru",
		"megafactory.client-a.svc.cluster.local",
	}
	for _, host := range public {
		if isManagedShardHost(host) {
			t.Errorf("isManagedShardHost(%q) = true, want false (this endpoint is meant to be held by clients)", host)
		}
	}
}

func hostTestShards() []shardAddr {
	return []shardAddr{
		{Name: "shard-1", Host: "postgresql.databases.svc.cluster.local", Port: 5432},
		{Name: "shard-0", Host: "pg-shard-0-postgresql.databases.svc.cluster.local", Port: 5432},
	}
}

// TestRouterReachesDatname_PlacedDatabase covers the ordinary case: a database
// the registry places on an addressed shard is reachable through the router, so
// its DSN may name the router instead of the instance.
func TestRouterReachesDatname_PlacedDatabase(t *testing.T) {
	placements := []dbPlacement{{Datname: "vibecoder", Shard: "shard-0"}}
	if !routerReachesDatname(hostTestShards(), placements, dbRouterDefaultShard, "vibecoder") {
		t.Fatal("routerReachesDatname = false for a database placed on an addressed shard, want true")
	}
}

// TestRouterReachesDatname_DefaultShardNeedsNoLine mirrors the renderer: a
// database on the default shard is served by the wildcard and gets no line of
// its own, which is still a correct route.
func TestRouterReachesDatname_DefaultShardNeedsNoLine(t *testing.T) {
	placements := []dbPlacement{{Datname: "megafactory", Shard: ""}}
	if !routerReachesDatname(hostTestShards(), placements, dbRouterDefaultShard, "megafactory") {
		t.Fatal("routerReachesDatname = false for a database on the default shard, want true")
	}
}

// TestRouterReachesDatname_UnknownIsNotReachable is the reason this predicate
// exists at all. The wildcard sends anything without a line to the default
// shard, so answering "reachable" for a database the registry cannot place
// would hand a client a DSN that connects successfully to the wrong instance --
// the failure mode of incident client-a/face-api (live connection, empty data).
func TestRouterReachesDatname_UnknownIsNotReachable(t *testing.T) {
	shards := hostTestShards()
	cases := []struct {
		name       string
		shards     []shardAddr
		placements []dbPlacement
		datname    string
	}{
		{
			name:       "no placement at all",
			shards:     shards,
			placements: nil,
			datname:    "orphan",
		},
		{
			name:   "same name on two shards, renderer drops it to the wildcard",
			shards: shards,
			placements: []dbPlacement{
				{Datname: "billing", Shard: "shard-0"},
				{Datname: "billing", Shard: "shard-1"},
			},
			datname: "billing",
		},
		{
			name:       "shard has no address, renderer drops it to the wildcard",
			shards:     []shardAddr{{Name: "shard-1", Host: "postgresql.databases.svc.cluster.local", Port: 5432}, {Name: "shard-2"}},
			placements: []dbPlacement{{Datname: "vibecoder", Shard: "shard-2"}},
			datname:    "vibecoder",
		},
		{
			name:       "default shard itself has no address, no table can be rendered",
			shards:     []shardAddr{{Name: "shard-0", Host: "pg-shard-0-postgresql.databases.svc.cluster.local", Port: 5432}},
			placements: []dbPlacement{{Datname: "vibecoder", Shard: "shard-0"}},
			datname:    "vibecoder",
		},
		{
			name:       "name the config grammar cannot express",
			shards:     shards,
			placements: []dbPlacement{{Datname: "bad name", Shard: "shard-0"}},
			datname:    "bad name",
		},
		{
			name:       "empty datname",
			shards:     shards,
			placements: []dbPlacement{{Datname: "vibecoder", Shard: "shard-0"}},
			datname:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if routerReachesDatname(tc.shards, tc.placements, dbRouterDefaultShard, tc.datname) {
				t.Fatal("routerReachesDatname = true, want false (the router cannot be trusted to land this client on the right instance)")
			}
		})
	}
}

// TestRouterReachesDatname_AgreesWithTheRenderer is the coupling check: the
// predicate decides whether a DSN may name the router, and the renderer decides
// where the router actually sends that name. If the two ever disagree, the DSN
// is wrong in the silent direction, so the predicate is asserted against the
// file the router really serves rather than against a second copy of the rule.
func TestRouterReachesDatname_AgreesWithTheRenderer(t *testing.T) {
	shards := hostTestShards()
	placements := []dbPlacement{
		{Datname: "vibecoder", Shard: "shard-0"},
		{Datname: "megafactory", Shard: "shard-1"},
	}
	out, _, err := renderPgBouncerRoutes(shards, placements, dbRouterDefaultShard)
	if err != nil {
		t.Fatalf("renderPgBouncerRoutes: %v", err)
	}
	if !strings.Contains(out, "vibecoder = host=pg-shard-0-postgresql.databases.svc.cluster.local") {
		t.Fatalf("rendered table has no line sending vibecoder to shard-0:\n%s", out)
	}
	if !routerReachesDatname(shards, placements, dbRouterDefaultShard, "vibecoder") {
		t.Fatal("the renderer routes vibecoder but the predicate says it is unreachable")
	}
}
