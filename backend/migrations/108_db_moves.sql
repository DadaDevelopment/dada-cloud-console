-- One in-flight move of a logical database from one shard to another.
--
-- The row is the move's only durable state: a restarted console picks a move
-- back up from its phase rather than starting over, because the expensive part
-- (the initial data copy) can run for hours on a 15 GB database and must not be
-- repeated because a pod rolled.
--
-- The row is also the authoritative placement while the move is finishing. The
-- routing table is otherwise rendered from the ServiceDatabaseV2 snapshot,
-- which lags a Crossplane reconcile behind reality; during a cutover that lag
-- is the window where clients would be sent back to the instance the data just
-- left. A 'done' move therefore overrides the snapshot until the CR catches up.
CREATE TABLE IF NOT EXISTS db_moves (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    datname       TEXT NOT NULL,
    owner_role    TEXT NOT NULL DEFAULT '',
    source_shard  TEXT NOT NULL,
    target_shard  TEXT NOT NULL,
    phase         TEXT NOT NULL DEFAULT 'pending',
    lag_bytes     BIGINT NOT NULL DEFAULT 0,
    error         TEXT NOT NULL DEFAULT '',
    requested_by  TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    cutover_at    TIMESTAMPTZ,
    CONSTRAINT db_moves_phase_check CHECK (
        phase IN ('pending', 'preparing', 'schema', 'syncing', 'cutover', 'done', 'failed')
    ),
    CONSTRAINT db_moves_shards_differ CHECK (source_shard <> target_shard)
);

-- A database has at most one unfinished move. Two concurrent moves of the same
-- database would each believe they own its routing entry, and the loser would
-- point the router at a shard whose copy stopped receiving changes.
CREATE UNIQUE INDEX IF NOT EXISTS db_moves_one_active
    ON db_moves (datname)
    WHERE phase NOT IN ('done', 'failed');

-- Lookup for the routing renderer: the newest finished move per database.
CREATE INDEX IF NOT EXISTS db_moves_datname_idx ON db_moves (datname, updated_at DESC);
