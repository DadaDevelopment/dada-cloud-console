-- Registry of PostgreSQL shards a managed database can live on.
--
-- A shard is one independent Postgres instance. Every logical database lives
-- entirely on one shard; the shard is written into ServiceDatabaseV2.spec.shard
-- at creation time and selects the provider-sql ProviderConfig the composition
-- uses for every object of that database. The registry is small (tens of rows),
-- so it lives in the control plane rather than in a coordinator like etcd.
--
-- Placement is automatic only. Nothing in the console offers a shard picker:
-- the row set decides where a new database lands, and moving an existing
-- database between shards is the documented data-move procedure (the data does
-- not follow the field).
CREATE TABLE IF NOT EXISTS db_shards (
    name             TEXT PRIMARY KEY,
    state            TEXT NOT NULL DEFAULT 'open',
    is_platform      BOOLEAN NOT NULL DEFAULT FALSE,
    capacity_bytes   BIGINT NOT NULL DEFAULT 0,
    metrics_selector TEXT NOT NULL DEFAULT '',
    note             TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT db_shards_state_check CHECK (state IN ('open', 'closed', 'draining'))
);

-- state: open = accepts new databases; closed = full or reserved (platform);
--        draining = databases are being moved off it, never receives new ones.
-- capacity_bytes: 0 = unbounded (the shard is bounded by its volume alone).
-- metrics_selector: PromQL label matcher that isolates this shard's series in
--        pg_database_size_bytes. Placement sums that vector to pick the emptiest
--        open shard. An empty or wrong selector yields no samples, which makes
--        the shard look empty rather than crashing placement, so the fallback
--        stays the default shard.

-- shard-1 is the existing shared instance (databases/postgresql). Every
-- database created before shards existed already lives there, and it stays the
-- default so nothing moves by the mere act of shipping this table.
INSERT INTO db_shards (name, state, is_platform, metrics_selector, note)
VALUES ('shard-1', 'open', FALSE, 'service="postgresql-metrics"',
        'Shared instance that predates sharding; default placement for tenants.')
ON CONFLICT (name) DO NOTHING;

-- shard-0 is reserved for platform databases (cloud-console, keycloak,
-- internal services). It is closed to tenant placement on purpose: the whole
-- point of the shard is that tenant traffic does not touch the instance the
-- control plane and SSO run on.
INSERT INTO db_shards (name, state, is_platform, metrics_selector, note)
VALUES ('shard-0', 'closed', TRUE, 'service="pg-shard-0-postgresql-metrics"',
        'Platform-only shard; tenant databases are never placed here.')
ON CONFLICT (name) DO NOTHING;
