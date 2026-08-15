CREATE TABLE IF NOT EXISTS app_url_http_seen (
    namespace      TEXT        NOT NULL,
    app_name       TEXT        NOT NULL,
    first_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace, app_name)
);
