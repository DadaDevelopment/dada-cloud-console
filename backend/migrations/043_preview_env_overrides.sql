-- 043_preview_env_overrides.sql
-- Preview-scoped env var overrides: rows live on the PARENT environment. When a
-- preview (PR) environment is created, a key present here wins over the value
-- inherited from the parent's env_vars for that same key. Keys that exist only
-- here (no env_vars counterpart) are copied into the preview env as ordinary
-- runtime vars. Deliberately a separate table (not a new env_vars.scope value)
-- so env_vars.scope keeps its existing build/runtime/both meaning and the
-- UNIQUE(environment_id, app_name, key) constraint on env_vars is untouched.
-- Overrides are durable on the parent env; the TTL reaper deletes the preview
-- environments row (cascading its own env_vars), never this table.
-- Forward-only, additive. Idempotent (matches the IF NOT EXISTS pattern used
-- since 014_preview_environments.sql).

CREATE TABLE IF NOT EXISTS preview_env_overrides (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    environment_id  UUID         NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    app_name        VARCHAR(255) NOT NULL,
    key             VARCHAR(255) NOT NULL,
    value_encrypted BYTEA        NOT NULL,
    is_secret       BOOLEAN      NOT NULL DEFAULT FALSE,
    created_by      UUID         REFERENCES users(id),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE(environment_id, app_name, key)
);

CREATE INDEX IF NOT EXISTS idx_preview_env_overrides_environment_app
    ON preview_env_overrides(environment_id, app_name);

GRANT SELECT, INSERT, UPDATE, DELETE ON preview_env_overrides TO dada;
