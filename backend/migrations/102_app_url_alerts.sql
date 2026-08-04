CREATE TABLE IF NOT EXISTS app_url_alerts (
    namespace             TEXT        NOT NULL,
    app_name              TEXT        NOT NULL,
    reason                TEXT,
    detail                TEXT,
    consecutive_failures  INT         NOT NULL DEFAULT 0,
    last_seen_at          TIMESTAMPTZ,
    last_sent_at          TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, app_name)
);

CREATE INDEX IF NOT EXISTS idx_app_url_alerts_last_seen_at ON app_url_alerts (last_seen_at);
