package config

import (
	"os"
	"testing"
)

// A shard named here loses the router indirection for every tenant database on
// it, so an unset variable must mean "no exceptions" rather than the shard the
// platform happened to start on.
func TestDirectShardsDefaultToNone(t *testing.T) {
	os.Unsetenv("DB_ROUTER_DIRECT_SHARDS")
	if got := splitList(os.Getenv("DB_ROUTER_DIRECT_SHARDS")); len(got) != 0 {
		t.Fatalf("direct shards without the variable = %v, want none", got)
	}
}

func TestDirectShardsAreReadFromTheVariable(t *testing.T) {
	t.Setenv("DB_ROUTER_DIRECT_SHARDS", "shard-0, shard-3")
	got := splitList(os.Getenv("DB_ROUTER_DIRECT_SHARDS"))
	if len(got) != 2 || got[0] != "shard-0" || got[1] != "shard-3" {
		t.Fatalf("direct shards = %v, want [shard-0 shard-3]", got)
	}
}
