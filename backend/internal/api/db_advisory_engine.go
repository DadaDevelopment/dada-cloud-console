package api

import (
	"context"
	"encoding/json"
	"log"
	"time"
)

// dbAdvisoryInterval is how often the rules re-run. Advisories describe trends
// measured in days, so a faster tick would only add write load; a slower one
// would let a fixed problem sit on the page long after the owner fixed it.
const dbAdvisoryInterval = 15 * time.Minute

// dbAdvisoryWindow is the lookback the rules see. Seven days is the shortest
// window in which "this index was never used" is a statement about behaviour
// rather than about a deploy that happened on Tuesday.
const dbAdvisoryWindow = 7 * 24 * time.Hour

// dbAdvisoryStaleAfter is how long a finding survives without being re-derived
// before it is deleted. Two ticks of slack absorbs a missed collection; beyond
// that, a rule that stopped firing means the problem is gone and the row has
// no business staying on the owner's page.
const dbAdvisoryStaleAfter = 2 * dbAdvisoryInterval

// dbAdvisoryEngine derives advisories from the samples the collector stored
// and keeps db_advisories in sync with them.
//
// It reads only the control-plane database. Nothing here touches a tenant
// instance: by the time a rule runs, every number it needs is already a row.
type dbAdvisoryEngine struct {
	h *Handler
}

// StartDBAdvisoryEngine launches the rules loop. Unlike the collector it needs
// no credentials — if no samples were ever collected it simply finds nothing.
func (h *Handler) StartDBAdvisoryEngine(ctx context.Context) {
	if h.pool == nil {
		return
	}
	e := &dbAdvisoryEngine{h: h}
	go func() {
		t := time.NewTicker(dbAdvisoryInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runWithAdvisoryLock(ctx, h.pool, lockKeyDBStatsCollect, "db-advisories", e.tick)
			}
		}
	}()
}

func (e *dbAdvisoryEngine) tick(ctx context.Context) {
	targets, err := e.targets(ctx)
	if err != nil {
		log.Printf("db-advisories: target lookup failed: %v", err)
		return
	}
	limits := e.limitsByDatname(ctx)

	total := 0
	for _, t := range targets {
		in, err := e.loadInput(ctx, t.shard, t.datname, limits[t.datname])
		if err != nil {
			log.Printf("db-advisories: load %s/%s: %v", t.shard, t.datname, err)
			continue
		}
		found := evaluateDBAdvisories(*in)
		if err := e.persist(ctx, t.shard, t.datname, found); err != nil {
			log.Printf("db-advisories: persist %s/%s: %v", t.shard, t.datname, err)
			continue
		}
		total += len(found)
	}
	log.Printf("db-advisories: tick databases=%d advisories=%d", len(targets), total)
}

type dbAdvisoryTarget struct {
	shard   string
	datname string
}

// targets lists the databases with a recent sample. A database that stopped
// being collected (dropped, or its shard lost credentials) drops out of the
// list and its advisories expire on their own through dbAdvisoryStaleAfter.
func (e *dbAdvisoryEngine) targets(ctx context.Context) ([]dbAdvisoryTarget, error) {
	rows, err := e.h.pool.Query(ctx, `
		SELECT DISTINCT shard, datname
		  FROM db_stat_databases
		 WHERE collected_at > NOW() - INTERVAL '1 hour'
		 ORDER BY shard, datname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []dbAdvisoryTarget
	for rows.Next() {
		var t dbAdvisoryTarget
		if err := rows.Scan(&t.shard, &t.datname); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// limitsByDatname maps a logical database to the storage quota its tier
// declares, so quota_forecast can put a date on the growth. A database with no
// managed record gets 0, which the rule reads as "no quota" and skips.
func (e *dbAdvisoryEngine) limitsByDatname(ctx context.Context) map[string]int64 {
	out := map[string]int64{}
	dbs, err := e.h.managedDatabasesForQuota(ctx)
	if err != nil {
		log.Printf("db-advisories: tier lookup failed: %v", err)
		return out
	}
	for _, d := range dbs {
		if d.Datname == "" {
			continue
		}
		out[d.Datname] = databaseTierLimitBytes[d.Tier]
	}
	return out
}

// loadInput assembles one database's window.
//
// Every delta is computed between the newest sample and the oldest sample that
// shares the newest one's stats_reset. A counter that still went backwards
// inside that window means a reset the view did not record, and the affected
// span is zeroed rather than clamped: a rule that sees Span == 0 declines to
// fire, which is the only safe reading of a counter nobody can trust.
func (e *dbAdvisoryEngine) loadInput(ctx context.Context, shard, datname string, limitBytes int64) (*dbAdvisoryInput, error) {
	in := &dbAdvisoryInput{
		Shard:      shard,
		Datname:    datname,
		Now:        time.Now().UTC(),
		LimitBytes: limitBytes,
	}

	var (
		lastAt     time.Time
		statsReset *time.Time
		instStart  *time.Time
		firstAt    time.Time
	)
	err := e.h.pool.QueryRow(ctx, `
		SELECT collected_at, size_bytes, stats_reset, instance_start_at
		  FROM db_stat_databases
		 WHERE shard = $1 AND datname = $2
		 ORDER BY collected_at DESC
		 LIMIT 1`, shard, datname).Scan(&lastAt, &in.SizeBytes, &statsReset, &instStart)
	if err != nil {
		return nil, err
	}
	if instStart != nil {
		in.Uptime = in.Now.Sub(instStart.UTC())
	}

	err = e.h.pool.QueryRow(ctx, `
		SELECT collected_at, size_bytes
		  FROM db_stat_databases
		 WHERE shard = $1 AND datname = $2
		   AND collected_at > $3
		   AND stats_reset IS NOT DISTINCT FROM $4
		 ORDER BY collected_at ASC
		 LIMIT 1`, shard, datname, in.Now.Add(-dbAdvisoryWindow), statsReset).
		Scan(&firstAt, &in.FirstSizeBytes)
	if err == nil {
		in.SizeSpan = lastAt.Sub(firstAt)
	}

	if err := e.loadTables(ctx, in, shard, datname); err != nil {
		return nil, err
	}
	if err := e.loadIndexes(ctx, in, shard, datname); err != nil {
		return nil, err
	}
	if err := e.loadStatements(ctx, in, shard, datname); err != nil {
		return nil, err
	}
	return in, nil
}

// dbAdvisoryTableSQL pairs each table's newest sample in the window with its
// oldest one in a single pass, so the loader does not issue a query per table.
const dbAdvisoryTableSQL = `
	WITH bounds AS (
		SELECT schemaname, relname,
		       MAX(collected_at) AS last_at,
		       MIN(collected_at) AS first_at
		  FROM db_stat_tables
		 WHERE shard = $1 AND datname = $2 AND collected_at > $3
		 GROUP BY schemaname, relname
	)
	SELECT b.schemaname, b.relname, b.last_at, b.first_at,
	       l.total_bytes, l.heap_bytes, l.index_bytes, l.rows_estimate,
	       l.n_tup_ins, l.n_tup_del, l.heap_blks_read, l.heap_blks_hit,
	       l.last_autoanalyze,
	       f.total_bytes, f.n_tup_ins, f.n_tup_del, f.heap_blks_read, f.heap_blks_hit
	  FROM bounds b
	  JOIN db_stat_tables l
	    ON l.shard = $1 AND l.datname = $2 AND l.schemaname = b.schemaname
	   AND l.relname = b.relname AND l.collected_at = b.last_at
	  JOIN db_stat_tables f
	    ON f.shard = $1 AND f.datname = $2 AND f.schemaname = b.schemaname
	   AND f.relname = b.relname AND f.collected_at = b.first_at`

func (e *dbAdvisoryEngine) loadTables(ctx context.Context, in *dbAdvisoryInput, shard, datname string) error {
	rows, err := e.h.pool.Query(ctx, dbAdvisoryTableSQL, shard, datname, in.Now.Add(-dbAdvisoryWindow))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			t                                       dbAdvisoryTable
			lastAt, firstAt                         time.Time
			lastIns, lastDel, lastRead, lastHit     int64
			firstBytes                              int64
			firstIns, firstDel, firstRead, firstHit int64
		)
		if err := rows.Scan(&t.Schema, &t.Name, &lastAt, &firstAt,
			&t.TotalBytes, &t.HeapBytes, &t.IndexBytes, &t.RowsEstimate,
			&lastIns, &lastDel, &lastRead, &lastHit, &t.LastAutoanalyze,
			&firstBytes, &firstIns, &firstDel, &firstRead, &firstHit); err != nil {
			return err
		}
		t.FirstTotalBytes = firstBytes
		t.Span = lastAt.Sub(firstAt)
		t.DeltaTupIns = lastIns - firstIns
		t.DeltaTupDel = lastDel - firstDel
		t.DeltaHeapRead = lastRead - firstRead
		t.DeltaHeapHit = lastHit - firstHit
		if t.DeltaTupIns < 0 || t.DeltaTupDel < 0 || t.DeltaHeapRead < 0 || t.DeltaHeapHit < 0 {
			t.Span = 0
			t.DeltaTupIns, t.DeltaTupDel, t.DeltaHeapRead, t.DeltaHeapHit = 0, 0, 0, 0
		}
		in.Tables = append(in.Tables, t)
	}
	return rows.Err()
}

// dbAdvisoryIndexSQL is the index-level counterpart of dbAdvisoryTableSQL.
const dbAdvisoryIndexSQL = `
	WITH bounds AS (
		SELECT schemaname, relname, indexrelname,
		       MAX(collected_at) AS last_at,
		       MIN(collected_at) AS first_at
		  FROM db_stat_indexes
		 WHERE shard = $1 AND datname = $2 AND collected_at > $3
		 GROUP BY schemaname, relname, indexrelname
	)
	SELECT b.schemaname, b.relname, b.indexrelname, b.last_at, b.first_at,
	       l.size_bytes, l.idx_scan, l.is_unique, l.is_primary, f.idx_scan
	  FROM bounds b
	  JOIN db_stat_indexes l
	    ON l.shard = $1 AND l.datname = $2 AND l.schemaname = b.schemaname
	   AND l.relname = b.relname AND l.indexrelname = b.indexrelname
	   AND l.collected_at = b.last_at
	  JOIN db_stat_indexes f
	    ON f.shard = $1 AND f.datname = $2 AND f.schemaname = b.schemaname
	   AND f.relname = b.relname AND f.indexrelname = b.indexrelname
	   AND f.collected_at = b.first_at`

func (e *dbAdvisoryEngine) loadIndexes(ctx context.Context, in *dbAdvisoryInput, shard, datname string) error {
	rows, err := e.h.pool.Query(ctx, dbAdvisoryIndexSQL, shard, datname, in.Now.Add(-dbAdvisoryWindow))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			idx             dbAdvisoryIndex
			lastAt, firstAt time.Time
			firstScans      int64
		)
		if err := rows.Scan(&idx.Schema, &idx.Table, &idx.Name, &lastAt, &firstAt,
			&idx.SizeBytes, &idx.LatestScans, &idx.IsUnique, &idx.IsPrimary, &firstScans); err != nil {
			return err
		}
		idx.Span = lastAt.Sub(firstAt)
		idx.DeltaScans = idx.LatestScans - firstScans
		if idx.DeltaScans < 0 {
			idx.Span = 0
			idx.DeltaScans = 0
		}
		in.Indexes = append(in.Indexes, idx)
	}
	return rows.Err()
}

// dbAdvisoryStatementSQL is the query-level counterpart of dbAdvisoryTableSQL.
const dbAdvisoryStatementSQL = `
	WITH bounds AS (
		SELECT queryid,
		       MAX(collected_at) AS last_at,
		       MIN(collected_at) AS first_at
		  FROM db_stat_statements
		 WHERE shard = $1 AND datname = $2 AND collected_at > $3
		 GROUP BY queryid
	)
	SELECT b.queryid, l.query_sample, l.mean_exec_ms,
	       l.calls, l.total_exec_ms, f.calls, f.total_exec_ms
	  FROM bounds b
	  JOIN db_stat_statements l
	    ON l.shard = $1 AND l.datname = $2 AND l.queryid = b.queryid
	   AND l.collected_at = b.last_at
	  JOIN db_stat_statements f
	    ON f.shard = $1 AND f.datname = $2 AND f.queryid = b.queryid
	   AND f.collected_at = b.first_at`

// loadStatements builds the per-query window.
//
// When the newest and oldest samples are the same row the delta is zero, and
// the loader falls back to the cumulative values instead. pg_stat_statements is
// itself cumulative since the last reset, so a single row already describes the
// whole period — the fallback is what lets the rules say something on the first
// tick rather than a week later.
func (e *dbAdvisoryEngine) loadStatements(ctx context.Context, in *dbAdvisoryInput, shard, datname string) error {
	rows, err := e.h.pool.Query(ctx, dbAdvisoryStatementSQL, shard, datname, in.Now.Add(-dbAdvisoryWindow))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			s                     dbAdvisoryStatement
			lastCalls, firstCalls int64
			lastMs, firstMs       float64
		)
		if err := rows.Scan(&s.QueryID, &s.Sample, &s.MeanMs,
			&lastCalls, &lastMs, &firstCalls, &firstMs); err != nil {
			return err
		}
		s.DeltaCalls = lastCalls - firstCalls
		s.DeltaTotalMs = lastMs - firstMs
		if s.DeltaCalls <= 0 || s.DeltaTotalMs <= 0 {
			s.DeltaCalls = lastCalls
			s.DeltaTotalMs = lastMs
		}
		in.Statements = append(in.Statements, s)
		in.StatementsTotalMs += s.DeltaTotalMs
	}
	return rows.Err()
}

// persist writes the current findings and removes the ones that stopped
// firing. Upsert rather than delete-and-insert so first_seen_at survives: the
// owner needs to know an advisory has been there for six days, and a row
// rewritten every quarter of an hour cannot tell them that.
func (e *dbAdvisoryEngine) persist(ctx context.Context, shard, datname string, found []dbAdvisory) error {
	now := time.Now().UTC()
	for _, a := range found {
		evidence, err := json.Marshal(a.Evidence)
		if err != nil {
			evidence = []byte(`{}`)
		}
		if _, err := e.h.pool.Exec(ctx, `
			INSERT INTO db_advisories (shard, datname, code, subject, severity,
				detail, suggested_sql, evidence, first_seen_at, last_seen_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
			ON CONFLICT (shard, datname, code, subject) DO UPDATE
			   SET severity = EXCLUDED.severity,
			       detail = EXCLUDED.detail,
			       suggested_sql = EXCLUDED.suggested_sql,
			       evidence = EXCLUDED.evidence,
			       last_seen_at = EXCLUDED.last_seen_at`,
			shard, datname, a.Code, a.Subject, a.Severity, a.Detail, a.SuggestedSQL,
			evidence, now); err != nil {
			return err
		}
	}
	_, err := e.h.pool.Exec(ctx, `
		DELETE FROM db_advisories
		 WHERE shard = $1 AND datname = $2 AND last_seen_at < $3`,
		shard, datname, now.Add(-dbAdvisoryStaleAfter))
	return err
}
