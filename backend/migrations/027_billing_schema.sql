CREATE TABLE IF NOT EXISTS billing_accounts (
    org_id          TEXT PRIMARY KEY,
    plan            TEXT NOT NULL DEFAULT 'free',
    plan_assigned_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS usage_records (
    id           UUID        NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    org_id       TEXT        NOT NULL,
    resource     TEXT        NOT NULL,
    used         INT         NOT NULL,
    period_start TIMESTAMPTZ NOT NULL,
    recorded_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, resource, period_start)
);

CREATE INDEX IF NOT EXISTS idx_usage_records_org_period
    ON usage_records (org_id, period_start);
