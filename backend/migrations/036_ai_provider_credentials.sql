CREATE TABLE IF NOT EXISTS ai_provider_credentials (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id         UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    provider           TEXT        NOT NULL,
    api_base           TEXT,
    api_key_encrypted  BYTEA       NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, provider)
);

CREATE INDEX IF NOT EXISTS idx_ai_provider_credentials_project
    ON ai_provider_credentials (project_id);
