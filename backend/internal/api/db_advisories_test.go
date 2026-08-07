package api

import (
	"strings"
	"testing"
	"time"
)

// oddsResearchInput replays the forensics that started this feature: the real
// numbers read off databases/postgresql-0 on 2026-08-06, before any of this
// code existed. Sizes, block counts and the query text are the measured ones,
// not illustrative values.
//
// It is the acceptance fixture. The rules have to find what two hours of
// manual work found, from the same data, with nobody pointing at the answer.
func oddsResearchInput() dbAdvisoryInput {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour

	return dbAdvisoryInput{
		Shard:   "shard-1",
		Datname: "odds-research",
		Now:     now,
		Uptime:  30 * 24 * time.Hour,

		SizeBytes:      15 << 30,
		FirstSizeBytes: 7456 << 20,
		SizeSpan:       36 * time.Hour,
		LimitBytes:     25 << 30,

		Tables: []dbAdvisoryTable{
			{
				Schema: "public", Name: "bookmaker_event_relationships",
				TotalBytes: 420 << 20, FirstTotalBytes: 300 << 20,
				RowsEstimate:  14_400_000,
				Span:          9 * time.Hour,
				DeltaTupIns:   4_000_000,
				DeltaTupDel:   0,
				DeltaHeapRead: 131_054_752,
				DeltaHeapHit:  8_262_500,
			},
			{
				Schema: "public", Name: "bookmaker_event_observations",
				TotalBytes: 3930 << 20, FirstTotalBytes: 1500 << 20,
				RowsEstimate:  14_400_000,
				Span:          48 * time.Hour,
				DeltaTupIns:   9_000_000,
				DeltaTupDel:   0,
				DeltaHeapRead: 17_158_016,
				DeltaHeapHit:  288_210,
			},
			{
				Schema: "public", Name: "bookmaker_selection_observations",
				TotalBytes: 5422 << 20, FirstTotalBytes: 2000 << 20,
				RowsEstimate:  14_400_000,
				Span:          48 * time.Hour,
				DeltaTupIns:   11_000_000,
				DeltaTupDel:   0,
				DeltaHeapRead: 509_329,
				DeltaHeapHit:  271_522,
			},
		},

		Indexes: []dbAdvisoryIndex{
			{
				Schema: "public", Table: "bookmaker_selection_observations",
				Name:      "bookmaker_selection_observations_pkey",
				SizeBytes: 562 << 20, LatestScans: 0, DeltaScans: 0,
				Span: week, IsPrimary: true, IsUnique: true,
			},
			{
				Schema: "public", Table: "bookmaker_event_relationships",
				Name:      "uq_bookmaker_event_relationship_observation",
				SizeBytes: 185 << 20, LatestScans: 0, DeltaScans: 0,
				Span: week, IsUnique: true,
			},
			{
				Schema: "public", Table: "bookmaker_event_relationships",
				Name:      "bookmaker_event_relationships_pkey",
				SizeBytes: 56 << 20, LatestScans: 0, DeltaScans: 0,
				Span: week, IsPrimary: true, IsUnique: true,
			},
			{
				Schema: "public", Table: "bookmaker_events",
				Name:      "bookmaker_events_pkey",
				SizeBytes: 90 << 20, LatestScans: 4_120_004, DeltaScans: 1_200_000,
				Span: week, IsPrimary: true, IsUnique: true,
			},
		},

		Statements: []dbAdvisoryStatement{
			{
				QueryID:      -4711282206658470000,
				Sample:       "SELECT DISTINCT ON (bookmaker_event_observations.bookmaker_event_id) ... WHERE bookmaker_event_observations.rule_set_id = $1::UUID",
				DeltaCalls:   1,
				DeltaTotalMs: 44_700,
				MeanMs:       44_700,
			},
			{
				QueryID:      8812993315017371000,
				Sample:       "SELECT ... FROM bookmaker_event_relationships WHERE relationship_type = $1 AND child_event_id IN ($2)",
				DeltaCalls:   12_400,
				DeltaTotalMs: 21_000,
				MeanMs:       1.7,
			},
			{
				QueryID:      1,
				Sample:       "SELECT 1",
				DeltaCalls:   90_000,
				DeltaTotalMs: 400,
				MeanMs:       0.004,
			},
		},
		StatementsTotalMs: 44_700 + 21_000 + 400,
	}
}

func advisoriesBySubject(list []dbAdvisory, code string) map[string]dbAdvisory {
	out := map[string]dbAdvisory{}
	for _, a := range list {
		if a.Code == code {
			out[a.Subject] = a
		}
	}
	return out
}

// TestEvaluateDBAdvisories_ReproducesOddsResearchForensics is the acceptance
// criterion from tasks/goal-db-insights.md. Failing it means the feature does
// not exist yet, whatever else has been written.
func TestEvaluateDBAdvisories_ReproducesOddsResearchForensics(t *testing.T) {
	found := evaluateDBAdvisories(oddsResearchInput())

	unused := advisoriesBySubject(found, dbAdvisoryUnusedIndex)
	for _, want := range []string{
		"public.bookmaker_selection_observations_pkey",
		"public.uq_bookmaker_event_relationship_observation",
		"public.bookmaker_event_relationships_pkey",
	} {
		if _, ok := unused[want]; !ok {
			t.Errorf("unused_index not reported for %s", want)
		}
	}
	if len(unused) != 3 {
		t.Errorf("unused_index count = %d, want exactly the three dead indexes: %v", len(unused), unused)
	}
	if _, ok := unused["public.bookmaker_events_pkey"]; ok {
		t.Error("unused_index fired on an index with 1.2M scans in the window")
	}

	appendOnly := advisoriesBySubject(found, dbAdvisoryAppendOnly)
	for _, want := range []string{
		"public.bookmaker_event_observations",
		"public.bookmaker_selection_observations",
	} {
		if _, ok := appendOnly[want]; !ok {
			t.Errorf("append_only_no_retention not reported for %s", want)
		}
	}
	if _, ok := appendOnly["public.bookmaker_event_relationships"]; ok {
		t.Error("append_only_no_retention extrapolated a weekly rate from a nine-hour window")
	}

	cache := advisoriesBySubject(found, dbAdvisoryLowCacheHit)
	got, ok := cache["public.bookmaker_event_relationships"]
	if !ok {
		t.Fatal("low_cache_hit not reported for bookmaker_event_relationships")
	}
	if got.Severity != dbAdvisoryCritical {
		t.Errorf("low_cache_hit severity = %s, want critical for a 6%% hit ratio on 1 TB of reads", got.Severity)
	}
	if ratio, _ := got.Evidence["hitRatio"].(float64); ratio > 0.07 {
		t.Errorf("hitRatio = %.4f, want ~0.06", ratio)
	}
	if _, ok := cache["public.bookmaker_selection_observations"]; ok {
		t.Error("low_cache_hit fired on a table below the absolute read floor")
	}

	slow := advisoriesBySubject(found, dbAdvisorySlowQuery)
	if _, ok := slow["query:-4711282206658470000"]; !ok {
		t.Error("slow_query not reported for the 44.7s DISTINCT ON query")
	}
	if _, ok := slow["query:1"]; ok {
		t.Error("slow_query fired on a 4-microsecond query")
	}
}

// TestUnusedIndexAdvisories_SilentAfterRestart pins the lesson the forensics
// taught: a counter that reads zero because the instance restarted is not
// evidence of anything. With a short uptime and a short window, the rule must
// say nothing at all rather than accuse three healthy indexes.
func TestUnusedIndexAdvisories_SilentAfterRestart(t *testing.T) {
	in := oddsResearchInput()
	in.Uptime = 9 * time.Hour
	for i := range in.Indexes {
		in.Indexes[i].Span = 9 * time.Hour
	}

	if got := unusedIndexAdvisories(in); len(got) != 0 {
		t.Errorf("unused_index fired %d times nine hours after a restart: %v", len(got), got)
	}
}

// TestStaleStatsAdvisories_RequireUptime covers the same trap on the other
// rule: every table on the fixture has a NULL last_autoanalyze, which looks
// identical to "never analyzed" and means nothing until the instance has been
// up longer than the window being claimed.
func TestStaleStatsAdvisories_RequireUptime(t *testing.T) {
	in := oddsResearchInput()
	in.Uptime = 9 * time.Hour
	if got := staleStatsAdvisories(in); len(got) != 0 {
		t.Errorf("stale_stats fired %d times on a freshly restarted instance: %v", len(got), got)
	}

	in.Uptime = 30 * 24 * time.Hour
	if got := staleStatsAdvisories(in); len(got) != len(in.Tables) {
		t.Errorf("stale_stats fired %d times, want %d after a month of uptime with no autoanalyze",
			len(got), len(in.Tables))
	}
}

// TestUnusedIndexAdvisories_ConstraintsGetNoDropSQL keeps the console from
// suggesting something PostgreSQL will refuse: a primary key or unique index
// is owned by a constraint and cannot be dropped with DROP INDEX.
func TestUnusedIndexAdvisories_ConstraintsGetNoDropSQL(t *testing.T) {
	in := oddsResearchInput()
	in.Indexes = append(in.Indexes, dbAdvisoryIndex{
		Schema: "public", Table: "t", Name: "idx_plain",
		SizeBytes: 100 << 20, Span: 7 * 24 * time.Hour,
	})

	for _, a := range unusedIndexAdvisories(in) {
		constrained, _ := a.Evidence["constraint"].(bool)
		if constrained && a.SuggestedSQL != "" {
			t.Errorf("%s is constraint-owned but got SQL %q", a.Subject, a.SuggestedSQL)
		}
		if !constrained && a.SuggestedSQL == "" {
			t.Errorf("%s is a plain index but got no DROP suggestion", a.Subject)
		}
	}
}

// TestQuotaForecastAdvisories_TurnsGrowthIntoADate checks the bridge to the
// tier ladder: the same growth that produces append_only_no_retention has to
// produce a date, because a quota nobody saw coming is the ticket this whole
// feature came from.
func TestQuotaForecastAdvisories_TurnsGrowthIntoADate(t *testing.T) {
	in := oddsResearchInput()
	got := quotaForecastAdvisories(in)
	if len(got) != 1 {
		t.Fatalf("quota_forecast returned %d advisories, want 1", len(got))
	}
	days, _ := got[0].Evidence["daysToLimit"].(float64)
	if days <= 0 || days > dbAdvisoryQuotaForecastDays {
		t.Errorf("daysToLimit = %.1f, want inside the forecast horizon", days)
	}

	in.LimitBytes = 0
	if got := quotaForecastAdvisories(in); len(got) != 0 {
		t.Errorf("quota_forecast fired with no quota configured: %v", got)
	}

	in = oddsResearchInput()
	in.SizeSpan = time.Hour
	if got := quotaForecastAdvisories(in); len(got) != 0 {
		t.Errorf("quota_forecast extrapolated from a one-hour window: %v", got)
	}
}

func TestParseShardAdminDSNs(t *testing.T) {
	got := parseShardAdminDSNs("shard-1=postgres://u:p@h:5432/postgres, shard-0=postgres://u:p@h2:5432/postgres,broken,=x,y=")
	if len(got) != 2 {
		t.Fatalf("parsed %d shards, want 2: %v", len(got), got)
	}
	if got["shard-1"] != "postgres://u:p@h:5432/postgres" {
		t.Errorf("shard-1 = %q", got["shard-1"])
	}
	if got["shard-0"] != "postgres://u:p@h2:5432/postgres" {
		t.Errorf("shard-0 = %q", got["shard-0"])
	}
	if len(parseShardAdminDSNs("")) != 0 {
		t.Error("empty configuration produced shards")
	}
}

// TestConfigForDatabase_ActuallyRetargets guards the trap this replaced:
// pgx.ConnConfig.ConnString returns the string it was parsed from, so
// rewriting the database and reading the DSN back yields the maintenance
// database again — and every tenant's tables would have been collected from
// postgres, under the tenant's name.
func TestConfigForDatabase_ActuallyRetargets(t *testing.T) {
	const dsn = "postgres://u:p@h:5432/postgres?sslmode=disable"

	cfg, err := configForDatabase(dsn, "odds-research")
	if err != nil {
		t.Fatalf("configForDatabase: %v", err)
	}
	if cfg.Database != "odds-research" {
		t.Errorf("Database = %q, want odds-research", cfg.Database)
	}
	if cfg.User != "u" || cfg.Host != "h" || cfg.Port != 5432 {
		t.Errorf("credentials lost: user=%q host=%q port=%d", cfg.User, cfg.Host, cfg.Port)
	}
	if strings.Contains(cfg.ConnString(), "odds-research") {
		t.Skip("pgx now rebuilds ConnString; the string path would be safe again")
	}
}

// TestIdleInTransactionAdvisories_NeedsTwoSamples pins the rule that separates
// a stuck transaction from an ordinary one. A single sample always catches
// somebody mid-transaction; only a transaction still open on the next tick is
// worth telling the owner about.
func TestIdleInTransactionAdvisories_NeedsTwoSamples(t *testing.T) {
	now := time.Now().UTC()
	one := dbAdvisoryInput{
		Datname: "app",
		Now:     now,
		Activity: []dbAdvisoryActivity{
			{At: now, Backends: 9, IdleInTxn: 1, MaxIdleTxnSecond: 900},
		},
	}
	if got := idleInTransactionAdvisories(one); len(got) != 0 {
		t.Fatalf("one sample fired: %+v", got)
	}

	gone := dbAdvisoryInput{
		Datname: "app",
		Now:     now,
		Activity: []dbAdvisoryActivity{
			{At: now, Backends: 9, IdleInTxn: 1, MaxIdleTxnSecond: 900},
			{At: now.Add(-5 * time.Minute), Backends: 9, IdleInTxn: 0},
		},
	}
	if got := idleInTransactionAdvisories(gone); len(got) != 0 {
		t.Fatalf("transaction seen once fired: %+v", got)
	}

	stuck := dbAdvisoryInput{
		Datname: "app",
		Now:     now,
		Activity: []dbAdvisoryActivity{
			{At: now, Backends: 9, IdleInTxn: 2, MaxIdleTxnSecond: 3000},
			{At: now.Add(-5 * time.Minute), Backends: 9, IdleInTxn: 1, MaxIdleTxnSecond: 2700},
		},
	}
	got := idleInTransactionAdvisories(stuck)
	if len(got) != 1 {
		t.Fatalf("two samples in a row produced %d advisories", len(got))
	}
	if got[0].Severity != dbAdvisoryCritical {
		t.Errorf("severity = %q, want critical for a 50 minute idle transaction", got[0].Severity)
	}
	if got[0].SuggestedSQL != "" {
		t.Errorf("rule handed out SQL: %q", got[0].SuggestedSQL)
	}
}

// TestIdleInTransactionAdvisories_ShortTransactionIsNotAFinding keeps the rule
// off healthy write traffic: a transaction that is briefly idle between
// statements is how every ORM works.
func TestIdleInTransactionAdvisories_ShortTransactionIsNotAFinding(t *testing.T) {
	now := time.Now().UTC()
	in := dbAdvisoryInput{
		Datname: "app",
		Now:     now,
		Activity: []dbAdvisoryActivity{
			{At: now, Backends: 4, IdleInTxn: 3, MaxIdleTxnSecond: 12},
			{At: now.Add(-5 * time.Minute), Backends: 4, IdleInTxn: 2, MaxIdleTxnSecond: 30},
		},
	}
	if got := idleInTransactionAdvisories(in); len(got) != 0 {
		t.Fatalf("short transactions fired: %+v", got)
	}
}
