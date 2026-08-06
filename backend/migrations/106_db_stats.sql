-- Raw statistics samples for Database Insights.
--
-- One collector walks every shard listed in db_shards, connects as the admin
-- role, and snapshots five system views per logical database. The rows here
-- are the only source both the tenant view ("what is happening in my
-- database") and the platform view ("who is eating this instance") read from:
-- pg_stat_statements is global per instance and tagged with dbid, so the same
-- query answers both questions.
--
-- Everything stored is the RAW CUMULATIVE counter as PostgreSQL reported it,
-- never a delta. Cumulative counters reset when the instance restarts, and a
-- collector that subtracts blindly draws negative traffic and invents idle
-- databases. Deltas are therefore computed at read time between two samples
-- that share the same stats_reset, and a window whose counter went backwards
-- is discarded rather than clamped. This is the odds-research lesson written
-- into the schema: last_autovacuum was NULL on every large table nine hours
-- after a pod restart, and that meant nothing at all.

-- Per logical database: pg_stat_database plus pg_database_size.
CREATE TABLE IF NOT EXISTS db_stat_databases (
    shard         TEXT        NOT NULL,
    datname       TEXT        NOT NULL,
    collected_at  TIMESTAMPTZ NOT NULL,
    size_bytes    BIGINT      NOT NULL DEFAULT 0,
    blks_read     BIGINT      NOT NULL DEFAULT 0,
    blks_hit      BIGINT      NOT NULL DEFAULT 0,
    xact_commit   BIGINT      NOT NULL DEFAULT 0,
    xact_rollback BIGINT      NOT NULL DEFAULT 0,
    tup_returned  BIGINT      NOT NULL DEFAULT 0,
    tup_fetched   BIGINT      NOT NULL DEFAULT 0,
    temp_bytes    BIGINT      NOT NULL DEFAULT 0,
    deadlocks     BIGINT      NOT NULL DEFAULT 0,
    numbackends   INTEGER     NOT NULL DEFAULT 0,
    -- stats_reset is the window guard: two samples are comparable only when
    -- this value matches. NULL means the instance never reset the view.
    stats_reset   TIMESTAMPTZ,
    -- instance_start_at makes "never vacuumed" answerable. An advisory that
    -- reads age from a counter may only fire when the instance has been up
    -- longer than the window it claims to measure.
    instance_start_at TIMESTAMPTZ,
    PRIMARY KEY (shard, datname, collected_at)
);

CREATE INDEX IF NOT EXISTS idx_db_stat_databases_time
    ON db_stat_databases (collected_at DESC);

-- Per table: pg_stat_user_tables joined with pg_statio_user_tables and the
-- relation sizes. Small tables are skipped by the collector; nothing here is
-- ever going to advise on a 40 kB lookup table.
CREATE TABLE IF NOT EXISTS db_stat_tables (
    shard           TEXT        NOT NULL,
    datname         TEXT        NOT NULL,
    schemaname      TEXT        NOT NULL,
    relname         TEXT        NOT NULL,
    collected_at    TIMESTAMPTZ NOT NULL,
    heap_bytes      BIGINT      NOT NULL DEFAULT 0,
    index_bytes     BIGINT      NOT NULL DEFAULT 0,
    total_bytes     BIGINT      NOT NULL DEFAULT 0,
    rows_estimate   BIGINT      NOT NULL DEFAULT 0,
    n_live_tup      BIGINT      NOT NULL DEFAULT 0,
    n_dead_tup      BIGINT      NOT NULL DEFAULT 0,
    n_tup_ins       BIGINT      NOT NULL DEFAULT 0,
    n_tup_upd       BIGINT      NOT NULL DEFAULT 0,
    n_tup_del       BIGINT      NOT NULL DEFAULT 0,
    seq_scan        BIGINT      NOT NULL DEFAULT 0,
    idx_scan        BIGINT      NOT NULL DEFAULT 0,
    heap_blks_read  BIGINT      NOT NULL DEFAULT 0,
    heap_blks_hit   BIGINT      NOT NULL DEFAULT 0,
    last_autovacuum  TIMESTAMPTZ,
    last_autoanalyze TIMESTAMPTZ,
    PRIMARY KEY (shard, datname, schemaname, relname, collected_at)
);

CREATE INDEX IF NOT EXISTS idx_db_stat_tables_db_time
    ON db_stat_tables (shard, datname, collected_at DESC);

-- Per index: pg_stat_user_indexes. idx_scan = 0 over a long enough window with
-- a matching stats_reset is the unused-index advisory, which on odds-research
-- alone accounts for ~800 MB that is only ever written to.
CREATE TABLE IF NOT EXISTS db_stat_indexes (
    shard        TEXT        NOT NULL,
    datname      TEXT        NOT NULL,
    schemaname   TEXT        NOT NULL,
    relname      TEXT        NOT NULL,
    indexrelname TEXT        NOT NULL,
    collected_at TIMESTAMPTZ NOT NULL,
    size_bytes   BIGINT      NOT NULL DEFAULT 0,
    idx_scan     BIGINT      NOT NULL DEFAULT 0,
    idx_tup_read BIGINT      NOT NULL DEFAULT 0,
    is_unique    BOOLEAN     NOT NULL DEFAULT FALSE,
    is_primary   BOOLEAN     NOT NULL DEFAULT FALSE,
    PRIMARY KEY (shard, datname, schemaname, relname, indexrelname, collected_at)
);

CREATE INDEX IF NOT EXISTS idx_db_stat_indexes_db_time
    ON db_stat_indexes (shard, datname, collected_at DESC);

-- Per normalized query: pg_stat_statements, filtered to one logical database
-- by dbid at collection time rather than at API time. A tenant must not be
-- able to reach another tenant's queryid even by accident, so the isolation
-- lives in the collector's WHERE clause.
CREATE TABLE IF NOT EXISTS db_stat_statements (
    shard             TEXT        NOT NULL,
    datname           TEXT        NOT NULL,
    queryid           BIGINT      NOT NULL,
    collected_at      TIMESTAMPTZ NOT NULL,
    calls             BIGINT      NOT NULL DEFAULT 0,
    total_exec_ms     DOUBLE PRECISION NOT NULL DEFAULT 0,
    mean_exec_ms      DOUBLE PRECISION NOT NULL DEFAULT 0,
    max_exec_ms       DOUBLE PRECISION NOT NULL DEFAULT 0,
    rows_returned     BIGINT      NOT NULL DEFAULT 0,
    shared_blks_read  BIGINT      NOT NULL DEFAULT 0,
    shared_blks_hit   BIGINT      NOT NULL DEFAULT 0,
    temp_blks_written BIGINT      NOT NULL DEFAULT 0,
    -- The text is already normalized by pg_stat_statements ($1 in place of
    -- constants). It is still the owner's data and is only ever served to the
    -- owner of the app the database belongs to.
    query_sample      TEXT        NOT NULL DEFAULT '',
    PRIMARY KEY (shard, datname, queryid, collected_at)
);

CREATE INDEX IF NOT EXISTS idx_db_stat_statements_db_time
    ON db_stat_statements (shard, datname, collected_at DESC);

-- Advisories derived from the samples above. Stored rather than computed per
-- request so that the console renders them inside the latency budget, so that
-- "first seen" is a real timestamp the owner can trust, and so that a rule
-- that stops firing disappears on its own instead of needing a cleanup pass.
CREATE TABLE IF NOT EXISTS db_advisories (
    shard        TEXT        NOT NULL,
    datname      TEXT        NOT NULL,
    code         TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    severity     TEXT        NOT NULL DEFAULT 'info',
    detail       TEXT        NOT NULL DEFAULT '',
    suggested_sql TEXT       NOT NULL DEFAULT '',
    -- Free-form evidence the rule fired on (sizes, ratios, dates). Kept so the
    -- console can render numbers without re-deriving them, and so a support
    -- session can tell why a rule fired three days ago.
    evidence     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (shard, datname, code, subject),
    CONSTRAINT db_advisories_severity_check
        CHECK (severity IN ('info', 'warning', 'critical'))
);

CREATE INDEX IF NOT EXISTS idx_db_advisories_db
    ON db_advisories (shard, datname, severity);
