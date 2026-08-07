package api

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"
)

// Advisory codes. The set is closed on purpose: every code is a rule someone
// can act on, and the console renders localized prose per code from the
// evidence map. A rule with no action attached to it is a graph, not an
// advisory, and belongs elsewhere on the page.
const (
	dbAdvisoryUnusedIndex   = "unused_index"
	dbAdvisoryStaleStats    = "stale_stats"
	dbAdvisoryAppendOnly    = "append_only_no_retention"
	dbAdvisoryLowCacheHit   = "low_cache_hit"
	dbAdvisorySlowQuery     = "slow_query"
	dbAdvisoryQuotaForecast = "quota_forecast"
)

const (
	dbAdvisoryInfo     = "info"
	dbAdvisoryWarning  = "warning"
	dbAdvisoryCritical = "critical"
)

// Rule thresholds, all derived from the odds-research forensics rather than
// invented: 562 MB of never-scanned index, a 6% cache hit ratio on a table
// read a hundred million blocks in nine hours, a single query holding 96% of
// the instance's execution time.
const (
	dbAdvisoryUnusedIndexWindow = 7 * 24 * time.Hour
	dbAdvisoryUnusedIndexBytes  = 50 << 20

	dbAdvisoryStaleStatsAge = 7 * 24 * time.Hour

	dbAdvisoryAppendOnlyWindow = 24 * time.Hour
	dbAdvisoryAppendOnlyGrowth = 1 << 30

	dbAdvisoryCacheHitFloor = 0.90
	dbAdvisoryCacheHitReads = 1_000_000

	dbAdvisorySlowQueryMeanMs = 1000
	dbAdvisorySlowQueryShare  = 0.20

	dbAdvisoryQuotaForecastDays = 30
)

// dbAdvisory is one finding about one subject inside one logical database.
//
// Detail is deliberately not prose: it is a compact, language-neutral
// statement of the numbers the rule fired on. The console renders localized
// text per code from Evidence, and the chat agent writes the narrative. A
// sentence hardcoded here would be wrong in one of the two console locales and
// redundant for the agent.
type dbAdvisory struct {
	Code         string
	Subject      string
	Severity     string
	Detail       string
	SuggestedSQL string
	Evidence     map[string]any
}

// dbAdvisoryTable is one table's window: latest sizes plus deltas across the
// window. Deltas are supplied by the loader, which discards any window where a
// counter went backwards — that is the reset guard, and it lives outside the
// rules so the rules can stay pure arithmetic.
type dbAdvisoryTable struct {
	Schema          string
	Name            string
	TotalBytes      int64
	HeapBytes       int64
	IndexBytes      int64
	RowsEstimate    int64
	FirstTotalBytes int64
	Span            time.Duration
	DeltaTupIns     int64
	DeltaTupDel     int64
	DeltaHeapRead   int64
	DeltaHeapHit    int64
	LastAutoanalyze *time.Time
}

// dbAdvisoryIndex is one index's window. DeltaScans is the number of scans
// observed across Span; LatestScans is the raw cumulative counter, used only
// on the uptime path below.
type dbAdvisoryIndex struct {
	Schema      string
	Table       string
	Name        string
	SizeBytes   int64
	DeltaScans  int64
	LatestScans int64
	Span        time.Duration
	IsPrimary   bool
	IsUnique    bool
}

// dbAdvisoryStatement is one normalized query's window.
type dbAdvisoryStatement struct {
	QueryID      int64
	Sample       string
	DeltaCalls   int64
	DeltaTotalMs float64
	MeanMs       float64
}

// dbAdvisoryInput is everything the rules see. Uptime is the instance's, not
// the database's, and it is what makes an absence of events meaningful: on
// odds-research every large table reported last_autovacuum = NULL nine hours
// after a pod restart, which looked exactly like "autovacuum never ran" and
// was not.
type dbAdvisoryInput struct {
	Shard             string
	Datname           string
	Now               time.Time
	Uptime            time.Duration
	SizeBytes         int64
	FirstSizeBytes    int64
	SizeSpan          time.Duration
	LimitBytes        int64
	Tables            []dbAdvisoryTable
	Indexes           []dbAdvisoryIndex
	Statements        []dbAdvisoryStatement
	StatementsTotalMs float64
}

// evaluateDBAdvisories runs every rule over one database's window and returns
// the findings, most severe first. Pure: no database, no clock, no network, so
// the odds-research numbers can be replayed as a fixture and the acceptance
// criterion checked without waiting a week for real samples.
func evaluateDBAdvisories(in dbAdvisoryInput) []dbAdvisory {
	var out []dbAdvisory
	out = append(out, unusedIndexAdvisories(in)...)
	out = append(out, staleStatsAdvisories(in)...)
	out = append(out, appendOnlyAdvisories(in)...)
	out = append(out, lowCacheHitAdvisories(in)...)
	out = append(out, slowQueryAdvisories(in)...)
	out = append(out, quotaForecastAdvisories(in)...)

	rank := map[string]int{dbAdvisoryCritical: 0, dbAdvisoryWarning: 1, dbAdvisoryInfo: 2}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Severity] != rank[out[j].Severity] {
			return rank[out[i].Severity] < rank[out[j].Severity]
		}
		return out[i].Subject < out[j].Subject
	})
	return out
}

// unusedIndexAdvisories reports large indexes nothing reads.
//
// Two independent paths make it fire, because either alone is wrong. The delta
// path (no scans observed across a window at least a week long) survives an
// instance restart mid-window but needs a week of collected samples. The
// uptime path (cumulative counter still zero, instance up longer than a week)
// works from the first sample but only once the instance has been up that
// long. A fresh install with a freshly restarted instance reports nothing,
// which is the correct answer rather than a convenient one.
//
// Primary keys and unique indexes are reported too: they cost the same write
// amplification and the same disk. They carry a constraint flag so the console
// never suggests DROP INDEX for something a constraint owns.
func unusedIndexAdvisories(in dbAdvisoryInput) []dbAdvisory {
	var out []dbAdvisory
	for _, idx := range in.Indexes {
		if idx.SizeBytes < dbAdvisoryUnusedIndexBytes {
			continue
		}
		byDelta := idx.Span >= dbAdvisoryUnusedIndexWindow && idx.DeltaScans == 0
		byUptime := in.Uptime >= dbAdvisoryUnusedIndexWindow && idx.LatestScans == 0
		if !byDelta && !byUptime {
			continue
		}
		constrained := idx.IsPrimary || idx.IsUnique
		sql := fmt.Sprintf("DROP INDEX CONCURRENTLY %s.%s;", idx.Schema, idx.Name)
		if constrained {
			sql = ""
		}
		out = append(out, dbAdvisory{
			Code:     dbAdvisoryUnusedIndex,
			Subject:  idx.Schema + "." + idx.Name,
			Severity: dbAdvisoryWarning,
			Detail: fmt.Sprintf("idx_scan=0 over %s, size=%s, table=%s.%s",
				humanDuration(maxDuration(idx.Span, in.Uptime)), humanBytes(idx.SizeBytes), idx.Schema, idx.Table),
			SuggestedSQL: sql,
			Evidence: map[string]any{
				"sizeBytes":   idx.SizeBytes,
				"table":       idx.Table,
				"windowHours": int(maxDuration(idx.Span, in.Uptime).Hours()),
				"constraint":  constrained,
				"isPrimary":   idx.IsPrimary,
				"isUnique":    idx.IsUnique,
			},
		})
	}
	return out
}

// staleStatsAdvisories reports tables the planner is guessing about.
//
// The uptime guard is the whole rule. Without it this fires on every table of
// every instance for the first week after any restart, which is both wrong and
// the fastest way to teach an owner to ignore the advisories panel.
func staleStatsAdvisories(in dbAdvisoryInput) []dbAdvisory {
	if in.Uptime < dbAdvisoryStaleStatsAge {
		return nil
	}
	var out []dbAdvisory
	for _, t := range in.Tables {
		age := dbAdvisoryStaleStatsAge + time.Hour
		if t.LastAutoanalyze != nil {
			age = in.Now.Sub(*t.LastAutoanalyze)
			if age < dbAdvisoryStaleStatsAge {
				continue
			}
		}
		out = append(out, dbAdvisory{
			Code:     dbAdvisoryStaleStats,
			Subject:  t.Schema + "." + t.Name,
			Severity: dbAdvisoryInfo,
			Detail: fmt.Sprintf("last_autoanalyze older than %s, rows=%s, uptime=%s",
				humanDuration(age), rowsEstimateText(t.RowsEstimate), humanDuration(in.Uptime)),
			SuggestedSQL: fmt.Sprintf("ANALYZE %s.%s;", t.Schema, t.Name),
			Evidence: map[string]any{
				"ageHours":    int(age.Hours()),
				"neverRan":    t.LastAutoanalyze == nil,
				"rows":        rowsEstimateValue(t.RowsEstimate),
				"uptimeHours": int(in.Uptime.Hours()),
			},
		})
	}
	return out
}

// appendOnlyAdvisories reports tables that only ever grow.
//
// This is not a complaint about the schema — append-only is a legitimate
// design. It is the early half of the quota conversation: a table with no
// deletions and a gigabyte a week of growth has a date attached to it, and the
// owner should meet that date on this page rather than at the write that fails.
func appendOnlyAdvisories(in dbAdvisoryInput) []dbAdvisory {
	var out []dbAdvisory
	for _, t := range in.Tables {
		if t.Span < dbAdvisoryAppendOnlyWindow || t.DeltaTupDel != 0 || t.DeltaTupIns <= 0 {
			continue
		}
		weekly := perWeek(float64(t.TotalBytes-t.FirstTotalBytes), t.Span)
		if weekly < dbAdvisoryAppendOnlyGrowth {
			continue
		}
		out = append(out, dbAdvisory{
			Code:     dbAdvisoryAppendOnly,
			Subject:  t.Schema + "." + t.Name,
			Severity: dbAdvisoryWarning,
			Detail: fmt.Sprintf("n_tup_del=0 over %s, +%d rows, growth=%s/week, size=%s",
				humanDuration(t.Span), t.DeltaTupIns, humanBytes(int64(weekly)), humanBytes(t.TotalBytes)),
			Evidence: map[string]any{
				"growthBytesPerWeek": int64(weekly),
				"totalBytes":         t.TotalBytes,
				"insertedRows":       t.DeltaTupIns,
				"deletedRows":        t.DeltaTupDel,
				"windowHours":        int(t.Span.Hours()),
			},
		})
	}
	return out
}

// lowCacheHitAdvisories reports tables being read off disk instead of memory.
//
// The read floor matters as much as the ratio: a table read four times, three
// of them from disk, has a 25% hit ratio and costs nothing. The pair
// (bad ratio, large absolute read volume) is what identifies the table that is
// making every neighbour on the shard slower.
func lowCacheHitAdvisories(in dbAdvisoryInput) []dbAdvisory {
	var out []dbAdvisory
	for _, t := range in.Tables {
		total := t.DeltaHeapRead + t.DeltaHeapHit
		if total <= 0 || t.DeltaHeapRead < dbAdvisoryCacheHitReads {
			continue
		}
		ratio := float64(t.DeltaHeapHit) / float64(total)
		if ratio >= dbAdvisoryCacheHitFloor {
			continue
		}
		readBytes := t.DeltaHeapRead * 8192
		severity := dbAdvisoryWarning
		if ratio < 0.5 && readBytes > 100<<30 {
			severity = dbAdvisoryCritical
		}
		out = append(out, dbAdvisory{
			Code:     dbAdvisoryLowCacheHit,
			Subject:  t.Schema + "." + t.Name,
			Severity: severity,
			Detail: fmt.Sprintf("cache hit %.1f%% over %s, %s read from disk, table size %s",
				ratio*100, humanDuration(t.Span), humanBytes(readBytes), humanBytes(t.TotalBytes)),
			Evidence: map[string]any{
				"hitRatio":       ratio,
				"blocksRead":     t.DeltaHeapRead,
				"bytesRead":      readBytes,
				"totalBytes":     t.TotalBytes,
				"windowHours":    int(t.Span.Hours()),
				"fitsInMemoryIf": t.TotalBytes,
			},
		})
	}
	return out
}

// slowQueryAdvisories reports the queries worth looking at: slow per call, or
// small per call but dominant in aggregate. Both matter and neither subsumes
// the other — on odds-research one query took 44.7 seconds in a single call
// while the database as a whole held 96% of the instance's execution time.
func slowQueryAdvisories(in dbAdvisoryInput) []dbAdvisory {
	var out []dbAdvisory
	for _, s := range in.Statements {
		share := 0.0
		if in.StatementsTotalMs > 0 {
			share = s.DeltaTotalMs / in.StatementsTotalMs
		}
		slow := s.MeanMs >= dbAdvisorySlowQueryMeanMs
		dominant := share >= dbAdvisorySlowQueryShare
		if !slow && !dominant {
			continue
		}
		severity := dbAdvisoryWarning
		if slow && dominant {
			severity = dbAdvisoryCritical
		}
		out = append(out, dbAdvisory{
			Code:     dbAdvisorySlowQuery,
			Subject:  fmt.Sprintf("query:%d", s.QueryID),
			Severity: severity,
			Detail: fmt.Sprintf("mean=%.0fms, calls=%d, share=%.1f%% of database time",
				s.MeanMs, s.DeltaCalls, share*100),
			Evidence: map[string]any{
				"queryId":     s.QueryID,
				"meanMs":      s.MeanMs,
				"calls":       s.DeltaCalls,
				"totalMs":     s.DeltaTotalMs,
				"share":       share,
				"querySample": s.Sample,
			},
		})
	}
	return out
}

// quotaForecastAdvisories turns the tier's storage limit from a wall into a
// date. Extrapolation is deliberately linear: a database that has been growing
// steadily for a week will keep doing so long enough for the owner to react,
// and a fancier model would only add confidence the data does not support.
func quotaForecastAdvisories(in dbAdvisoryInput) []dbAdvisory {
	if in.LimitBytes <= 0 || in.SizeSpan < dbAdvisoryAppendOnlyWindow {
		return nil
	}
	growth := float64(in.SizeBytes - in.FirstSizeBytes)
	if growth <= 0 {
		return nil
	}
	perDay := growth / (float64(in.SizeSpan) / float64(24*time.Hour))
	if perDay <= 0 {
		return nil
	}
	remaining := float64(in.LimitBytes - in.SizeBytes)
	if remaining < 0 {
		remaining = 0
	}
	days := remaining / perDay
	if days > dbAdvisoryQuotaForecastDays || math.IsInf(days, 0) || math.IsNaN(days) {
		return nil
	}
	severity := dbAdvisoryWarning
	if days <= 7 {
		severity = dbAdvisoryCritical
	}
	return []dbAdvisory{{
		Code:     dbAdvisoryQuotaForecast,
		Subject:  in.Datname,
		Severity: severity,
		Detail: fmt.Sprintf("%s of %s used, +%s/day, limit reached in %.0f days",
			humanBytes(in.SizeBytes), humanBytes(in.LimitBytes), humanBytes(int64(perDay)), days),
		Evidence: map[string]any{
			"sizeBytes":         in.SizeBytes,
			"limitBytes":        in.LimitBytes,
			"growthBytesPerDay": int64(perDay),
			"daysToLimit":       days,
			"exhaustedAt":       in.Now.Add(time.Duration(days * float64(24*time.Hour))).Format(time.RFC3339),
		},
	}}
}

// perWeek scales an observed change to a weekly rate. A span of zero yields
// zero rather than an infinity, so a single sample can never produce a
// forecast.
func perWeek(delta float64, span time.Duration) float64 {
	if span <= 0 {
		return 0
	}
	return delta * (float64(7*24*time.Hour) / float64(span))
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// rowsEstimateText renders reltuples for a human. PostgreSQL 14 and later write
// -1 for a relation that has never been analyzed, which is not a row count and
// must not be shown as one — on odds-research it would have claimed a table
// holds minus one row.
func rowsEstimateText(rows int64) string {
	if rows < 0 {
		return "unknown"
	}
	return strconv.FormatInt(rows, 10)
}

// rowsEstimateValue is the same distinction for the evidence map: an unanalyzed
// relation carries no estimate at all rather than a negative one.
func rowsEstimateValue(rows int64) any {
	if rows < 0 {
		return nil
	}
	return rows
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTP"[exp])
}

func humanDuration(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%.0fd", d.Hours()/24)
	case d >= time.Hour:
		return fmt.Sprintf("%.0fh", d.Hours())
	default:
		return fmt.Sprintf("%.0fm", d.Minutes())
	}
}
