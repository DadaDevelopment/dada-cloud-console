-- Storage-quota state for managed databases (ServiceDatabaseV2).
--
-- The quota worker decides one state per database and drives it into git via a
-- SetDatabaseEnforcement operation. The decision has to survive a pod restart
-- and be shared by both replicas, so it lives here and not in memory: the row
-- is what tells the next tick "this database is already read-only, do not
-- enqueue the same operation again", and what tells the console why a database
-- refuses writes.
CREATE TABLE IF NOT EXISTS db_quota_state (
    environment_id UUID NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    project_id     UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    tier           TEXT NOT NULL DEFAULT 'unlimited',
    limit_bytes    BIGINT NOT NULL DEFAULT 0,
    size_bytes     BIGINT NOT NULL DEFAULT 0,
    ratio          DOUBLE PRECISION NOT NULL DEFAULT 0,
    state          TEXT NOT NULL DEFAULT 'none',
    state_since    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_warn_at   TIMESTAMPTZ,
    last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (environment_id, name)
);

CREATE INDEX IF NOT EXISTS idx_db_quota_state_project ON db_quota_state (project_id);
CREATE INDEX IF NOT EXISTS idx_db_quota_state_enforced ON db_quota_state (state) WHERE state <> 'none';
