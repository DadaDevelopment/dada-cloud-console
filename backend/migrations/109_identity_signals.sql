CREATE TABLE IF NOT EXISTS identity_signals (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event           TEXT NOT NULL,
    observed_ip     TEXT,
    user_agent      TEXT,
    ua_family       TEXT,
    accept_language TEXT,
    client_hints    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS identity_signals_user_idx ON identity_signals (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS identity_signals_created_idx ON identity_signals (created_at DESC);
CREATE INDEX IF NOT EXISTS identity_signals_ua_idx ON identity_signals (ua_family, created_at DESC);
