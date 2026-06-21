-- 016_monitoring.sql
-- Monitoring resource kind (PRD-monitoring, ADR-011). A monitoring app is an
-- ingest target inside a project: an external device/service that pushes metrics
-- (-> Prometheus remote-write) and logs (-> Elasticsearch dada-app-logs-*).
-- Each carries a scoped API key (metrics:write, logs:write).
--
-- The plaintext key is shown once at creation; only an argon2id hash + a short
-- displayable prefix are persisted (the gateway verifies the key out-of-band when
-- it exchanges it for fat claims; the hash is kept for local verify/rotation).
-- Forward-only, additive, idempotent.

CREATE TABLE IF NOT EXISTS monitoring_apps (
    id             UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id     UUID         NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_id UUID         NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name           VARCHAR(255) NOT NULL,
    api_key_prefix VARCHAR(24),
    api_key_hash   BYTEA,
    scopes         TEXT[]       NOT NULL DEFAULT ARRAY['metrics:write','logs:write'],
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE(project_id, environment_id, name)
);

CREATE INDEX IF NOT EXISTS idx_monitoring_apps_project ON monitoring_apps(project_id);
