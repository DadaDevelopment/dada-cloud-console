-- 024_monitoring_key_prefix_index.sql
-- Telemetry Gateway (ADR-012): the gateway resolves the monitoring resource
-- from the dmon_ key alone (no appId in the OTLP path), via a prefix-indexed
-- candidate lookup -> constant-time argon2id verify. This index makes the
-- WHERE api_key_prefix = $1 lookup a point read instead of a full scan.
-- The prefix is the narrow (collisions allowed); argon2id is the decider.
-- Forward-only, additive, idempotent.

CREATE INDEX IF NOT EXISTS idx_monitoring_apps_api_key_prefix
    ON monitoring_apps (api_key_prefix);
