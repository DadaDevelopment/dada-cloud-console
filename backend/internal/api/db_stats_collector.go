package api

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Collector cadence. Statement and per-database counters move every second and
// are cheap to read (two views on the maintenance connection), so they tick
// often. Table and index sizes move slowly and cost one connection per logical
// database, so they tick every dbStatsDeepEvery-th pass.
const (
	dbStatsCollectInterval = 5 * time.Minute
	dbStatsDeepEvery       = 3
)

// Collection caps. Nothing here is meant to inventory a schema: the product
// question is "what is big, hot, or wasteful", and everything below these
// thresholds is neither. Caps also bound the write volume, which is the real
// cost of the feature.
const (
	dbStatsMinTableBytes = 1 << 20
	dbStatsMinIndexBytes = 1 << 20
	dbStatsMaxTables     = 100
	dbStatsMaxIndexes    = 200
	dbStatsMaxStatements = 25
)

// Retention. Raw samples answer "what changed this week"; anything older is
// answered by advisories, which carry their own first_seen_at. Statements are
// the highest-volume table and the least useful once stale, so they go first.
const (
	dbStatsRetention           = 14 * 24 * time.Hour
	dbStatsStatementsRetention = 7 * 24 * time.Hour
	dbStatsPruneEvery          = time.Hour
)

// dbStatsCollector snapshots PostgreSQL system views from every shard that has
// admin credentials configured and stores the raw cumulative counters.
//
// It never writes to a tenant database. The single exception is CREATE
// EXTENSION IF NOT EXISTS pg_stat_statements in the instance's maintenance
// database, which is an instance-level admin action on our own shard, not DDL
// on anyone's data.
type dbStatsCollector struct {
	h         *Handler
	dsns      map[string]string
	ticks     int
	lastPrune time.Time
}

// parseShardAdminDSNs reads DB_SHARD_ADMIN_DSNS: comma-separated
// "shard=dsn" pairs. Malformed entries are dropped rather than fatal — a typo
// in one shard's credentials must not take insights down for the others.
//
// Pure, so the format is testable without a database.
func parseShardAdminDSNs(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, dsn, ok := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		dsn = strings.TrimSpace(dsn)
		if !ok || name == "" || dsn == "" {
			continue
		}
		out[name] = dsn
	}
	return out
}

// configForDatabase points a shard's admin DSN at one logical database. The
// collector needs a connection per database because pg_stat_user_tables is
// database-local, and the credentials are the same for all of them.
//
// It returns a parsed config rather than a rewritten string on purpose:
// pgx's ConnString reports the connection string it was parsed from, so a
// round trip through it would silently keep connecting to the maintenance
// database and report its statistics under every tenant's name.
func configForDatabase(dsn, datname string) (*pgx.ConnConfig, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.Database = datname
	return cfg, nil
}

// StartDBStatsCollector launches the insights collector. No credentials means
// no collection: a deployment that has not been given shard access runs
// exactly as it does today, with the console showing no insights rather than
// erroring.
func (h *Handler) StartDBStatsCollector(ctx context.Context) {
	if h.pool == nil || h.cfg == nil {
		return
	}
	dsns := parseShardAdminDSNs(h.cfg.DBShardAdminDSNs)
	if len(dsns) == 0 {
		return
	}
	c := &dbStatsCollector{h: h, dsns: dsns}
	log.Printf("db-stats: collector started interval=%s shards=%d", dbStatsCollectInterval, len(dsns))
	go func() {
		runWithAdvisoryLock(ctx, h.pool, lockKeyDBStatsCollect, "db-stats", c.tick)
		t := time.NewTicker(dbStatsCollectInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runWithAdvisoryLock(ctx, h.pool, lockKeyDBStatsCollect, "db-stats", c.tick)
			}
		}
	}()
}

// tick collects every configured shard once. A shard that fails is logged and
// skipped: shards are independent instances, and one unreachable instance must
// not cost the others their sample.
func (c *dbStatsCollector) tick(ctx context.Context) {
	c.ticks++
	deep := c.ticks%dbStatsDeepEvery == 1

	names := make([]string, 0, len(c.dsns))
	for name := range c.dsns {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, shard := range names {
		if err := c.collectShard(ctx, shard, c.dsns[shard], deep); err != nil {
			log.Printf("db-stats: shard %s failed: %v", shard, err)
		}
	}

	if time.Since(c.lastPrune) >= dbStatsPruneEvery {
		c.prune(ctx)
		c.lastPrune = time.Now()
	}
}

// dbStatDatabaseRow is one pg_stat_database sample, carrying the two fields
// that make any later delta trustworthy: statsReset, which invalidates a
// window when it moves, and instanceStart, without which "never vacuumed"
// cannot be told apart from "restarted an hour ago".
type dbStatDatabaseRow struct {
	datname       string
	sizeBytes     int64
	blksRead      int64
	blksHit       int64
	xactCommit    int64
	xactRollback  int64
	tupReturned   int64
	tupFetched    int64
	tempBytes     int64
	deadlocks     int64
	numbackends   int32
	statsReset    *time.Time
	instanceStart *time.Time
}

func (c *dbStatsCollector) collectShard(ctx context.Context, shard, dsn string, deep bool) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(context.Background())

	now := time.Now().UTC()

	dbs, err := c.collectDatabases(ctx, conn, shard, now)
	if err != nil {
		return fmt.Errorf("databases: %w", err)
	}
	if err := c.collectStatements(ctx, conn, shard, now); err != nil {
		log.Printf("db-stats: shard %s statements: %v", shard, err)
	}
	if !deep {
		return nil
	}
	for _, db := range dbs {
		dbCfg, err := configForDatabase(dsn, db.datname)
		if err != nil {
			log.Printf("db-stats: shard %s db %s dsn: %v", shard, db.datname, err)
			continue
		}
		if err := c.collectRelations(ctx, shard, db.datname, dbCfg, now); err != nil {
			log.Printf("db-stats: shard %s db %s relations: %v", shard, db.datname, err)
		}
	}
	return nil
}

func (c *dbStatsCollector) collectDatabases(ctx context.Context, conn *pgx.Conn, shard string, now time.Time) ([]dbStatDatabaseRow, error) {
	rows, err := conn.Query(ctx, `
		SELECT d.datname,
		       pg_database_size(d.oid),
		       s.blks_read, s.blks_hit, s.xact_commit, s.xact_rollback,
		       s.tup_returned, s.tup_fetched, s.temp_bytes, s.deadlocks,
		       s.numbackends, s.stats_reset, pg_postmaster_start_time()
		  FROM pg_database d
		  JOIN pg_stat_database s ON s.datid = d.oid
		 WHERE d.datallowconn AND NOT d.datistemplate`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []dbStatDatabaseRow
	for rows.Next() {
		var r dbStatDatabaseRow
		if err := rows.Scan(&r.datname, &r.sizeBytes, &r.blksRead, &r.blksHit,
			&r.xactCommit, &r.xactRollback, &r.tupReturned, &r.tupFetched,
			&r.tempBytes, &r.deadlocks, &r.numbackends, &r.statsReset, &r.instanceStart); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	batch := &pgx.Batch{}
	for _, r := range out {
		batch.Queue(`
			INSERT INTO db_stat_databases (shard, datname, collected_at, size_bytes,
				blks_read, blks_hit, xact_commit, xact_rollback, tup_returned,
				tup_fetched, temp_bytes, deadlocks, numbackends, stats_reset,
				instance_start_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			ON CONFLICT DO NOTHING`,
			shard, r.datname, now, r.sizeBytes, r.blksRead, r.blksHit,
			r.xactCommit, r.xactRollback, r.tupReturned, r.tupFetched,
			r.tempBytes, r.deadlocks, r.numbackends, r.statsReset, r.instanceStart)
	}
	return out, c.sendBatch(ctx, batch)
}

// collectStatements reads the instance-global pg_stat_statements and splits it
// per logical database by dbid. The split happens here, in the collector's
// join, and not at API time: a tenant must not be able to reach another
// tenant's queryid even through a bug in an endpoint.
func (c *dbStatsCollector) collectStatements(ctx context.Context, conn *pgx.Conn, shard string, now time.Time) error {
	if _, err := conn.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pg_stat_statements`); err != nil {
		return fmt.Errorf("extension unavailable: %w", err)
	}
	rows, err := conn.Query(ctx, `
		SELECT d.datname, s.queryid, s.calls, s.total_exec_time, s.mean_exec_time,
		       s.max_exec_time, s.rows, s.shared_blks_read, s.shared_blks_hit,
		       s.temp_blks_written, left(s.query, 4096)
		  FROM pg_stat_statements s
		  JOIN pg_database d ON d.oid = s.dbid
		 WHERE s.queryid IS NOT NULL
		   AND d.datallowconn AND NOT d.datistemplate`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type stmt struct {
		datname   string
		queryid   int64
		calls     int64
		totalMs   float64
		meanMs    float64
		maxMs     float64
		rowsRet   int64
		blksRead  int64
		blksHit   int64
		tempWrite int64
		sample    string
	}
	perDB := map[string][]stmt{}
	for rows.Next() {
		var s stmt
		if err := rows.Scan(&s.datname, &s.queryid, &s.calls, &s.totalMs, &s.meanMs,
			&s.maxMs, &s.rowsRet, &s.blksRead, &s.blksHit, &s.tempWrite, &s.sample); err != nil {
			return err
		}
		perDB[s.datname] = append(perDB[s.datname], s)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	batch := &pgx.Batch{}
	for _, list := range perDB {
		sort.Slice(list, func(i, j int) bool { return list[i].totalMs > list[j].totalMs })
		if len(list) > dbStatsMaxStatements {
			list = list[:dbStatsMaxStatements]
		}
		for _, s := range list {
			batch.Queue(`
				INSERT INTO db_stat_statements (shard, datname, queryid, collected_at,
					calls, total_exec_ms, mean_exec_ms, max_exec_ms, rows_returned,
					shared_blks_read, shared_blks_hit, temp_blks_written, query_sample)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
				ON CONFLICT DO NOTHING`,
				shard, s.datname, s.queryid, now, s.calls, s.totalMs, s.meanMs,
				s.maxMs, s.rowsRet, s.blksRead, s.blksHit, s.tempWrite, s.sample)
		}
	}
	return c.sendBatch(ctx, batch)
}

// collectRelations opens one connection to a single logical database and reads
// its table and index statistics. Read-only by construction: every statement
// here is a SELECT against a system view.
func (c *dbStatsCollector) collectRelations(ctx context.Context, shard, datname string, cfg *pgx.ConnConfig, now time.Time) error {
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(context.Background())

	batch := &pgx.Batch{}

	tableRows, err := conn.Query(ctx, `
		SELECT t.schemaname, t.relname,
		       pg_table_size(c.oid), pg_indexes_size(c.oid), pg_total_relation_size(c.oid),
		       GREATEST(c.reltuples, 0)::bigint,
		       t.n_live_tup, t.n_dead_tup, t.n_tup_ins, t.n_tup_upd, t.n_tup_del,
		       t.seq_scan, COALESCE(t.idx_scan, 0),
		       COALESCE(io.heap_blks_read, 0), COALESCE(io.heap_blks_hit, 0),
		       t.last_autovacuum, t.last_autoanalyze
		  FROM pg_stat_user_tables t
		  JOIN pg_class c ON c.oid = t.relid
		  LEFT JOIN pg_statio_user_tables io ON io.relid = t.relid
		 WHERE pg_total_relation_size(c.oid) >= $1
		 ORDER BY pg_total_relation_size(c.oid) DESC
		 LIMIT $2`, int64(dbStatsMinTableBytes), dbStatsMaxTables)
	if err != nil {
		return fmt.Errorf("tables: %w", err)
	}
	for tableRows.Next() {
		var (
			schemaName, relName                         string
			heap, idxBytes, total, reltuples            int64
			live, dead, ins, upd, del, seqScan, idxScan int64
			heapRead, heapHit                           int64
			lastVacuum, lastAnalyze                     *time.Time
		)
		if err := tableRows.Scan(&schemaName, &relName, &heap, &idxBytes, &total, &reltuples,
			&live, &dead, &ins, &upd, &del, &seqScan, &idxScan,
			&heapRead, &heapHit, &lastVacuum, &lastAnalyze); err != nil {
			tableRows.Close()
			return fmt.Errorf("tables scan: %w", err)
		}
		batch.Queue(`
			INSERT INTO db_stat_tables (shard, datname, schemaname, relname, collected_at,
				heap_bytes, index_bytes, total_bytes, rows_estimate, n_live_tup, n_dead_tup,
				n_tup_ins, n_tup_upd, n_tup_del, seq_scan, idx_scan,
				heap_blks_read, heap_blks_hit, last_autovacuum, last_autoanalyze)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
			ON CONFLICT DO NOTHING`,
			shard, datname, schemaName, relName, now, heap, idxBytes, total, reltuples,
			live, dead, ins, upd, del, seqScan, idxScan, heapRead, heapHit,
			lastVacuum, lastAnalyze)
	}
	tableRows.Close()
	if err := tableRows.Err(); err != nil {
		return fmt.Errorf("tables: %w", err)
	}

	indexRows, err := conn.Query(ctx, `
		SELECT i.schemaname, i.relname, i.indexrelname,
		       pg_relation_size(i.indexrelid), COALESCE(i.idx_scan, 0),
		       COALESCE(i.idx_tup_read, 0), ix.indisunique, ix.indisprimary
		  FROM pg_stat_user_indexes i
		  JOIN pg_index ix ON ix.indexrelid = i.indexrelid
		 WHERE pg_relation_size(i.indexrelid) >= $1
		 ORDER BY pg_relation_size(i.indexrelid) DESC
		 LIMIT $2`, int64(dbStatsMinIndexBytes), dbStatsMaxIndexes)
	if err != nil {
		return fmt.Errorf("indexes: %w", err)
	}
	for indexRows.Next() {
		var (
			schemaName, relName, indexName string
			size, scans, tupRead           int64
			unique, primary                bool
		)
		if err := indexRows.Scan(&schemaName, &relName, &indexName, &size, &scans,
			&tupRead, &unique, &primary); err != nil {
			indexRows.Close()
			return fmt.Errorf("indexes scan: %w", err)
		}
		batch.Queue(`
			INSERT INTO db_stat_indexes (shard, datname, schemaname, relname, indexrelname,
				collected_at, size_bytes, idx_scan, idx_tup_read, is_unique, is_primary)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT DO NOTHING`,
			shard, datname, schemaName, relName, indexName, now, size, scans,
			tupRead, unique, primary)
	}
	indexRows.Close()
	if err := indexRows.Err(); err != nil {
		return fmt.Errorf("indexes: %w", err)
	}

	return c.sendBatch(ctx, batch)
}

// sendBatch writes one collection unit to the control-plane database. An empty
// batch is skipped so an idle shard costs no round trip.
func (c *dbStatsCollector) sendBatch(ctx context.Context, batch *pgx.Batch) error {
	if batch.Len() == 0 {
		return nil
	}
	br := c.h.pool.SendBatch(ctx, batch)
	defer br.Close()
	for i := 0; i < batch.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (c *dbStatsCollector) prune(ctx context.Context) {
	cutoff := time.Now().Add(-dbStatsRetention)
	stmtCutoff := time.Now().Add(-dbStatsStatementsRetention)
	for _, q := range []struct {
		sql string
		at  time.Time
	}{
		{`DELETE FROM db_stat_databases WHERE collected_at < $1`, cutoff},
		{`DELETE FROM db_stat_tables WHERE collected_at < $1`, cutoff},
		{`DELETE FROM db_stat_indexes WHERE collected_at < $1`, cutoff},
		{`DELETE FROM db_stat_statements WHERE collected_at < $1`, stmtCutoff},
	} {
		if _, err := c.h.pool.Exec(ctx, q.sql, q.at); err != nil {
			log.Printf("db-stats: prune failed: %v", err)
		}
	}
}
