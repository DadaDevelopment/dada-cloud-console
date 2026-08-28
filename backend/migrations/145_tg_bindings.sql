-- Telegram <-> kagent agent gateway (tg-gateway, backend/cmd/tg-gateway):
-- one row per agent bound to a Telegram bot. tg-gateway owns this table
-- exclusively -- the console backend never reads or writes it directly, it
-- proxies through tg-gateway's internal HTTP API (POST/DELETE/GET /bindings),
-- the same posture kagent.Reader has toward the kagent runtime.
--
-- bot_token is stored plaintext by design (see the design doc's "why not
-- git" section): it is a live secret with no config-drift story, simplest as
-- one row nobody else reads. status is text rather than a bool so a future
-- "paused" or "revoked" state does not need a schema change. No FK to
-- projects(id): tg-gateway's own (narrower) DB role is not expected to hold
-- rights on that table, same split as the telemetry gateway's GATEWAY_DB_URL.
CREATE TABLE IF NOT EXISTS tg_bindings (
    agent_name    TEXT        NOT NULL PRIMARY KEY,
    project_id    UUID        NOT NULL,
    bot_token     TEXT        NOT NULL,
    bot_username  TEXT        NOT NULL DEFAULT '',
    status        TEXT        NOT NULL DEFAULT 'active',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tg_bindings_project ON tg_bindings (project_id);

-- The main role until a narrower tg-gateway-only role is provisioned at the
-- infra level (TG_GATEWAY_DB_URL then falls back to the shared DB_URL, which
-- needs this grant to work at all).
GRANT SELECT, INSERT, UPDATE, DELETE ON tg_bindings TO dada;
