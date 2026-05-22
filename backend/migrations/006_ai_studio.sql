-- 006_ai_studio.sql
-- AI Studio (v2): AIModel deploys via AIModel XRD + KServe.
-- See docs/plans/2026-05-22-v2-ai-studio.md.
-- Forward-only, additive. Idempotent.

-- ① ai_storage_prefix on projects
-- Backend enforces artifactURI prefix isolation (D13).
ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS ai_storage_prefix VARCHAR(500);

-- ② project_quotas
-- First real quota table; reusable for future managed services.
CREATE TABLE IF NOT EXISTS project_quotas (
    project_id              UUID         PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    cpu_model_max           INTEGER      NOT NULL DEFAULT 5,
    gpu_model_max           INTEGER      NOT NULL DEFAULT 0,        -- 0 forces approval (D12)
    monthly_inference_calls BIGINT       NOT NULL DEFAULT 100000,    -- advisory cap
    updated_at              TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- ③ aimodel_api_keys
-- Per-model API key for the embedded PublicApi (D8).
-- Plaintext is generated once at worker time, hashed (argon2id) here,
-- and the plaintext is parked in a short-lived reveal row (see ④).
CREATE TABLE IF NOT EXISTS aimodel_api_keys (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      UUID         NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_id  UUID         NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    aimodel_name    VARCHAR(255) NOT NULL,
    key_prefix      VARCHAR(16)  NOT NULL,        -- first 8 chars, displayable
    key_hash        BYTEA        NOT NULL,        -- argon2id of full key
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    revoked_at      TIMESTAMPTZ
);

-- One active (non-revoked) key per (project, environment, model).
-- Partial unique index allows rotation: revoke old, insert new.
CREATE UNIQUE INDEX IF NOT EXISTS idx_aimodel_api_keys_active
    ON aimodel_api_keys(project_id, environment_id, aimodel_name)
    WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_aimodel_api_keys_project
    ON aimodel_api_keys(project_id);

-- ④ aimodel_api_key_reveals
-- Short-lived (15 min) plaintext-key parking, keyed by operation id.
-- Consumed exactly once via GET /api-key?reveal=true; deleted on consume
-- and by a periodic cleanup. After expiry the key is unrecoverable —
-- only rotate (D17, S6 risk).
CREATE TABLE IF NOT EXISTS aimodel_api_key_reveals (
    operation_id   UUID         PRIMARY KEY REFERENCES operations(id) ON DELETE CASCADE,
    api_key_id     UUID         NOT NULL REFERENCES aimodel_api_keys(id) ON DELETE CASCADE,
    plaintext      BYTEA        NOT NULL,        -- raw key bytes; deleted on reveal/expiry
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    expires_at     TIMESTAMPTZ  NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_aimodel_api_key_reveals_expires_at
    ON aimodel_api_key_reveals(expires_at);

-- ⑤ aimodel_inference_counters
-- Bucketed call counters for the advisory monthly budget (T2/D12).
-- Written by the inference proxy only; production traffic via PublicApi
-- bypasses this counter until v2 metrics ingestion.
CREATE TABLE IF NOT EXISTS aimodel_inference_counters (
    project_id      UUID         NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    year_month      VARCHAR(7)   NOT NULL,             -- '2026-05'
    aimodel_name    VARCHAR(255) NOT NULL,
    environment_id  UUID         NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    call_count      BIGINT       NOT NULL DEFAULT 0,
    last_called_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (project_id, year_month, aimodel_name, environment_id)
);
CREATE INDEX IF NOT EXISTS idx_aimodel_inference_counters_project_month
    ON aimodel_inference_counters(project_id, year_month);

-- ⑥ Grants for the dada app user (mirrors 005_grant_dada_user.sql).
-- The default-privileges ALTER from 005 should already cover these,
-- but be explicit for tables created here.
GRANT SELECT, INSERT, UPDATE, DELETE ON
    project_quotas,
    aimodel_api_keys,
    aimodel_api_key_reveals,
    aimodel_inference_counters
TO dada;
