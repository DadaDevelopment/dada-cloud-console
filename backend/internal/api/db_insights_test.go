package api

import "testing"

// TestServiceDatabaseShard_DefaultsToTheSharedInstance pins the reading of a
// snapshot written before shards existed. Reading such a database as "no
// shard" would silently return an empty insights page for every database
// created before the sharding migration, which is most of them.
func TestServiceDatabaseShard_DefaultsToTheSharedInstance(t *testing.T) {
	cases := []struct {
		name    string
		summary string
		want    string
	}{
		{"pre-sharding snapshot", `{"spec":{"database":"odds-research"}}`, dbInsightsDefaultShard},
		{"reconciled live field", `{"shard":"shard-2","spec":{"database":"x"}}`, "shard-2"},
		{"spec placement", `{"spec":{"database":"x","shard":"shard-3"}}`, "shard-3"},
		{"empty live field", `{"shard":"","spec":{"database":"x"}}`, dbInsightsDefaultShard},
		{"garbage", `not json`, dbInsightsDefaultShard},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := serviceDatabaseShard([]byte(tc.summary)); got != tc.want {
				t.Errorf("serviceDatabaseShard(%s) = %q, want %q", tc.summary, got, tc.want)
			}
		})
	}
}

// TestServiceDatabaseTier_MatchesTheQuotaWatcher keeps the insights page and
// the enforcement ladder reading the same field. If they diverged, the page
// would show a quota the watcher does not enforce.
func TestServiceDatabaseTier_MatchesTheQuotaWatcher(t *testing.T) {
	if got := serviceDatabaseTier([]byte(`{"tier":"business"}`)); got != "business" {
		t.Errorf("tier = %q, want business", got)
	}
	if got := serviceDatabaseTier([]byte(`{"spec":{"database":"x"}}`)); got != "unlimited" {
		t.Errorf("absent tier = %q, want unlimited", got)
	}
	if _, ok := databaseTierLimitBytes[serviceDatabaseTier([]byte(`{}`))]; !ok {
		t.Error("the default tier has no entry in databaseTierLimitBytes")
	}
}
