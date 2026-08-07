-- Per-database connection state, sampled alongside the other statistics.
--
-- Only counts and durations are stored, never the text of a running query.
-- pg_stat_activity.query is the statement as the client sent it, constants and
-- all: it is the owner's data, and the samples here are read by platform-wide
-- views. The live view on the database page reads pg_stat_activity directly
-- when the owner opens it, so the raw text never has to be persisted to be
-- shown to the person who owns it.
--
-- Two samples in a row carrying a long-lived idle transaction is what the
-- idle_in_transaction advisory fires on: one sample cannot tell a transaction
-- that is stuck apart from one that happened to be open when the tick landed.
CREATE TABLE IF NOT EXISTS db_stat_activity (
    shard        TEXT        NOT NULL,
    datname      TEXT        NOT NULL,
    collected_at TIMESTAMPTZ NOT NULL,
    backends     INTEGER     NOT NULL DEFAULT 0,
    active       INTEGER     NOT NULL DEFAULT 0,
    idle         INTEGER     NOT NULL DEFAULT 0,
    idle_in_txn  INTEGER     NOT NULL DEFAULT 0,
    waiting      INTEGER     NOT NULL DEFAULT 0,
    -- Age of the oldest transaction still open, whatever its state. A long
    -- one blocks vacuum from reclaiming dead rows across the whole instance,
    -- which is why this is measured per database but judged per instance.
    max_xact_seconds     DOUBLE PRECISION NOT NULL DEFAULT 0,
    -- Age of the oldest transaction that is open and doing nothing.
    max_idle_txn_seconds DOUBLE PRECISION NOT NULL DEFAULT 0,
    PRIMARY KEY (shard, datname, collected_at)
);

CREATE INDEX IF NOT EXISTS idx_db_stat_activity_recent
    ON db_stat_activity (shard, datname, collected_at DESC);
