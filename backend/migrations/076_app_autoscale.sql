-- Vertical autoscaler state, one row per (namespace, app). Mirrors the
-- app_volume_alerts / app_health_alerts shape so the cooldown survives pod
-- restarts and is shared across console replicas, rather than living in memory.
--
-- last_sent_at gates how often an app may be resized (a resize triggers a
-- rollout, so it must not fire every tick), and is claimed atomically. It is
-- deliberately separate from last_seen_at, which records "still over threshold
-- right now" on every tick so the console can distinguish an app that is still
-- starved from one that was resized hours ago.
CREATE TABLE IF NOT EXISTS app_autoscale_events (
    namespace    TEXT        NOT NULL,
    app_name     TEXT        NOT NULL,
    last_sent_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    from_profile TEXT        NOT NULL DEFAULT '',
    to_profile   TEXT        NOT NULL DEFAULT '',
    reason       TEXT        NOT NULL DEFAULT '',
    ratio        DOUBLE PRECISION,
    PRIMARY KEY (namespace, app_name)
);

-- Supports the console's "what did the autoscaler do lately" read without a
-- sequential scan once the table grows past a few hundred apps.
CREATE INDEX IF NOT EXISTS idx_app_autoscale_events_sent
    ON app_autoscale_events (last_sent_at DESC);
