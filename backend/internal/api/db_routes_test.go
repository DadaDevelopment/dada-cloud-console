package api

import (
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/metrics"
)

func routeShards() []shardAddr {
	return []shardAddr{
		{Name: "shard-1", Host: "postgresql.databases.svc.cluster.local", Port: 5432},
		{Name: "shard-0", Host: "pg-shard-0-postgresql.databases.svc.cluster.local", Port: 5432},
		{Name: "shard-2", Host: "pg-shard-2-postgresql.databases.svc.cluster.local", Port: 5432},
	}
}

func TestRenderRoutesWildcardOnlyWhenNothingMoved(t *testing.T) {
	out, stats, err := renderPgBouncerRoutes(routeShards(), []dbPlacement{
		{Datname: "app-one", Shard: "shard-1"},
		{Datname: "app-two", Shard: ""},
	}, "shard-1")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "* = host=postgresql.databases.svc.cluster.local port=5432 auth_dbname=postgres") {
		t.Fatalf("wildcard missing:\n%s", out)
	}
	if strings.Contains(out, "app-one =") || strings.Contains(out, "app-two =") {
		t.Fatalf("databases on the default shard must not get a line:\n%s", out)
	}
	if stats.Routed != 0 || len(stats.Dropped) != 0 {
		t.Fatalf("nothing moved, so nothing is routed or dropped: %+v", stats)
	}
	if stats.PerShard["shard-1"] != 2 {
		t.Fatalf("a placement without a shard counts against the default: %+v", stats.PerShard)
	}
}

func TestRenderRoutesLinesMovedDatabase(t *testing.T) {
	out, stats, err := renderPgBouncerRoutes(routeShards(), []dbPlacement{
		{Datname: "odds-research", Shard: "shard-2"},
		{Datname: "app-one", Shard: "shard-1"},
	}, "shard-1")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "odds-research = host=pg-shard-2-postgresql.databases.svc.cluster.local port=5432 dbname=odds-research auth_dbname=postgres"
	if !strings.Contains(out, want) {
		t.Fatalf("missing route for the moved database:\n%s", out)
	}
	if strings.Contains(out, "user=") {
		t.Fatalf("a user= field breaks SCRAM pass-through:\n%s", out)
	}
	if stats.Routed != 1 {
		t.Fatalf("the moved database is the one routed by name: %+v", stats)
	}
}

func TestRenderRoutesDropsAmbiguousName(t *testing.T) {
	out, stats, err := renderPgBouncerRoutes(routeShards(), []dbPlacement{
		{Datname: "billing", Shard: "shard-2"},
		{Datname: "billing", Shard: "shard-0"},
	}, "shard-1")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(out, "billing = ") {
		t.Fatalf("a name living on two shards has no correct route:\n%s", out)
	}
	if !strings.Contains(out, "; billing: same name on 2 shards") {
		t.Fatalf("the dropped name must be visible in the file:\n%s", out)
	}
	if stats.Dropped[metrics.RouteDropAmbiguousName] != 1 {
		t.Fatalf("an ambiguous name must be countable, it reaches the wrong shard: %+v", stats.Dropped)
	}
}

func TestRenderRoutesSkipsShardWithoutAddress(t *testing.T) {
	shards := append(routeShards(), shardAddr{Name: "shard-3", Host: "", Port: 5432})
	out, stats, err := renderPgBouncerRoutes(shards, []dbPlacement{
		{Datname: "fresh", Shard: "shard-3"},
	}, "shard-1")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(out, "fresh = ") {
		t.Fatalf("an unaddressable shard must fall back to the wildcard:\n%s", out)
	}
	if !strings.Contains(out, "; fresh: shard shard-3 has no address") {
		t.Fatalf("missing note about the unaddressable shard:\n%s", out)
	}
	if stats.Dropped[metrics.RouteDropShardUnaddresed] != 1 {
		t.Fatalf("an unaddressable shard must be countable: %+v", stats.Dropped)
	}
}

func TestRenderRoutesFailsWithoutDefaultAddress(t *testing.T) {
	if _, _, err := renderPgBouncerRoutes([]shardAddr{
		{Name: "shard-2", Host: "pg-shard-2-postgresql.databases.svc.cluster.local", Port: 5432},
	}, []dbPlacement{{Datname: "odds-research", Shard: "shard-2"}}, "shard-1"); err == nil {
		t.Fatal("a table without the wildcard would reject every unlisted database")
	}
}

func TestRenderRoutesDefaultsPort(t *testing.T) {
	out, stats, err := renderPgBouncerRoutes([]shardAddr{
		{Name: "shard-1", Host: "postgresql.databases.svc.cluster.local"},
	}, nil, "shard-1")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "port=5432") {
		t.Fatalf("missing default port:\n%s", out)
	}
	if stats.Routed != 0 {
		t.Fatalf("no placements, no routes: %+v", stats)
	}
}

func TestRenderRoutesRejectsUnsafeName(t *testing.T) {
	out, stats, err := renderPgBouncerRoutes(routeShards(), []dbPlacement{
		{Datname: "evil = host=attacker.example\n*", Shard: "shard-2"},
	}, "shard-1")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(out, "attacker.example") {
		t.Fatalf("a name must never be able to rewrite the table:\n%s", out)
	}
	if stats.Dropped[metrics.RouteDropUnsafeName] != 1 {
		t.Fatalf("a rejected name must be countable: %+v", stats.Dropped)
	}
}

func TestSafeRouteToken(t *testing.T) {
	for _, ok := range []string{"odds-research", "app_1", "A0"} {
		if !safeRouteToken(ok) {
			t.Errorf("safeRouteToken(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "a b", "a=b", "a\nb", "a;b", strings.Repeat("x", 64)} {
		if safeRouteToken(bad) {
			t.Errorf("safeRouteToken(%q) = true, want false", bad)
		}
	}
}

func TestApplyMoveOverridesOutranksSnapshot(t *testing.T) {
	got := applyMoveOverrides([]dbPlacement{
		{Datname: "odds-research", Shard: "shard-1"},
		{Datname: "app-one", Shard: "shard-1"},
	}, map[string]string{"odds-research": "shard-2"})

	if got[0].Shard != "shard-2" {
		t.Fatalf("a database that has cut over must route to its new shard: %+v", got[0])
	}
	if got[1].Shard != "shard-1" {
		t.Fatalf("a database with no move must not be touched: %+v", got[1])
	}
}

func TestApplyMoveOverridesKeepsInputIntact(t *testing.T) {
	in := []dbPlacement{{Datname: "odds-research", Shard: "shard-1"}}
	applyMoveOverrides(in, map[string]string{"odds-research": "shard-2"})
	if in[0].Shard != "shard-1" {
		t.Fatal("the snapshot slice is shared with the caller and must not be rewritten in place")
	}
}

func TestApplyMoveOverridesAddsDatabaseWithoutSnapshot(t *testing.T) {
	got := applyMoveOverrides([]dbPlacement{
		{Datname: "app-one", Shard: "shard-1"},
	}, map[string]string{"no-cr-yet": "shard-0"})

	if len(got) != 2 {
		t.Fatalf("a move without a snapshot row must still be routed: %+v", got)
	}
	if got[1].Datname != "no-cr-yet" || got[1].Shard != "shard-0" {
		t.Fatalf("wrong placement added: %+v", got[1])
	}
}

func TestRenderRoutesLinesMoveWithoutSnapshot(t *testing.T) {
	out, stats, err := renderPgBouncerRoutes(routeShards(),
		applyMoveOverrides(nil, map[string]string{"no-cr-yet": "shard-0"}), "shard-1")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "no-cr-yet = host=pg-shard-0-postgresql.databases.svc.cluster.local port=5432 dbname=no-cr-yet auth_dbname=postgres"
	if !strings.Contains(out, want) {
		t.Fatalf("a cut-over database with no CR must get a line:\n%s", out)
	}
	if stats.Routed != 1 {
		t.Fatalf("the moved database is the one routed by name: %+v", stats)
	}
}
