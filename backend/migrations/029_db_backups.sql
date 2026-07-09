-- Per-database logical backup catalog. The console drives per-DB Postgres
-- backup/restore imperatively via Kanister ActionSets (K10 policies cannot pass a
-- per-database option); this table is the source of truth the console lists from,
-- since K10 produces no per-database restore points.

CREATE TABLE IF NOT EXISTS db_backups (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id     UUID NOT NULL REFERENCES projects(id),
    environment_id UUID NOT NULL REFERENCES environments(id),
    resource_name  VARCHAR(255) NOT NULL,
    database_name  VARCHAR(255) NOT NULL,
    dump_path      TEXT NOT NULL,
    size_bytes     BIGINT,
    status         VARCHAR(32)  NOT NULL DEFAULT 'Pending',
    kind           VARCHAR(32)  NOT NULL DEFAULT 'manual',
    action_set     VARCHAR(255),
    error_message  TEXT,
    created_by     UUID REFERENCES users(id),
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    expires_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_db_backups_lookup
    ON db_backups(project_id, environment_id, resource_name, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_db_backups_active
    ON db_backups(status) WHERE status IN ('Pending', 'Running', 'Deleting');
