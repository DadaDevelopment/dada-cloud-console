-- One archive of a table's history: the rows older than a cutoff date are
-- written to Parquet on S3, verified, deleted, and the space returned with
-- pg_repack.
--
-- The row is the run's only durable state, for the same reason db_moves is:
-- the expensive step (the export) runs for minutes to hours inside a
-- Kubernetes Job, and a console pod that rolls mid-run must resume at the
-- phase it reached rather than export the same rows twice or - far worse -
-- delete rows whose export it never confirmed.
--
-- The phase order encodes the safety property: nothing is deleted before
-- 'verify' has passed, and 'verify' is a separate step precisely so the
-- delete can never be the thing that discovers the export was short.
CREATE TABLE IF NOT EXISTS db_archive_runs (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id     UUID NOT NULL,
    environment_id UUID NOT NULL,
    resource_name  TEXT NOT NULL,
    datname        TEXT NOT NULL,
    shard          TEXT NOT NULL,
    schema_name    TEXT NOT NULL DEFAULT 'public',
    table_name     TEXT NOT NULL,
    cutoff_column  TEXT NOT NULL,
    cutoff_date    DATE NOT NULL,
    phase          TEXT NOT NULL DEFAULT 'pending',
    planned_rows   BIGINT NOT NULL DEFAULT 0,
    exported_rows  BIGINT NOT NULL DEFAULT 0,
    deleted_rows   BIGINT NOT NULL DEFAULT 0,
    bytes_estimate BIGINT NOT NULL DEFAULT 0,
    bytes_freed    BIGINT NOT NULL DEFAULT 0,
    bucket         TEXT NOT NULL DEFAULT '',
    s3_uri         TEXT NOT NULL DEFAULT '',
    manifest       JSONB NOT NULL DEFAULT '{}'::jsonb,
    error          TEXT NOT NULL DEFAULT '',
    requested_by   TEXT NOT NULL DEFAULT '',
    auto           BOOLEAN NOT NULL DEFAULT FALSE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at    TIMESTAMPTZ,
    CONSTRAINT db_archive_runs_phase_check CHECK (
        phase IN ('pending', 'sink', 'export', 'verify', 'delete', 'repack', 'done', 'failed')
    )
);

-- One unfinished run per table. Two concurrent runs over the same table would
-- export overlapping ranges and then each delete what the other is still
-- verifying.
CREATE UNIQUE INDEX IF NOT EXISTS db_archive_runs_one_active
    ON db_archive_runs (datname, schema_name, table_name)
    WHERE phase NOT IN ('done', 'failed');

-- The console lists a database's archive history newest first, and the worker
-- reads every unfinished run on each tick.
CREATE INDEX IF NOT EXISTS db_archive_runs_database_idx
    ON db_archive_runs (project_id, environment_id, resource_name, created_at DESC);
CREATE INDEX IF NOT EXISTS db_archive_runs_open_idx
    ON db_archive_runs (created_at)
    WHERE phase NOT IN ('done', 'failed');
