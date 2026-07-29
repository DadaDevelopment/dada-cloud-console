CREATE TABLE IF NOT EXISTS app_volume_alerts (
    namespace    TEXT        NOT NULL,
    app_name     TEXT        NOT NULL,
    last_sent_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, app_name)
);
