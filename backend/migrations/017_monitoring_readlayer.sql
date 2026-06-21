-- 017_monitoring_readlayer.sql
-- Alerts / dashboards / health layer on top of the monitoring_apps resource
-- (created in 016 by the ingestion chip). Additive, idempotent, forward-only.
--
-- Grafana owns alert evaluation/routing/dashboards (ADR-011); these tables are a
-- lightweight Postgres mirror so the console can render the native list UI and
-- compute health without round-tripping Grafana on every request. Grafana stays
-- the source of truth — the mirror is reconciled best-effort.

-- Per-resource health thresholds + the deterministic Grafana object handles
-- (folder is per-project, dashboard is per-resource). Added to the existing
-- monitoring_apps row rather than a side table to keep the resource self-contained.
ALTER TABLE monitoring_apps
    ADD COLUMN IF NOT EXISTS health_config         JSONB       NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS grafana_folder_uid    VARCHAR(64),
    ADD COLUMN IF NOT EXISTS grafana_dashboard_uid VARCHAR(64);

-- Native Grafana contact points, project-scoped. Mirror only; secret settings
-- (bot tokens, SMTP) live write-only in Grafana, never persisted here.
CREATE TABLE IF NOT EXISTS monitoring_channels (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id               UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name                     VARCHAR(255) NOT NULL,
    type                     VARCHAR(20)  NOT NULL CHECK (type IN ('telegram','email','webhook')),
    grafana_contactpoint_uid VARCHAR(64),
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(project_id, name)
);

CREATE INDEX IF NOT EXISTS idx_monitoring_channels_project ON monitoring_channels(project_id);

-- Lightweight mirror of Grafana alert rules for the native list UI.
CREATE TABLE IF NOT EXISTS monitoring_alert_rules (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    monitoring_app_id UUID NOT NULL REFERENCES monitoring_apps(id) ON DELETE CASCADE,
    channel_id        UUID REFERENCES monitoring_channels(id) ON DELETE SET NULL,
    name              VARCHAR(255) NOT NULL,
    metric            VARCHAR(255) NOT NULL,   -- metric name (cpu, memory, temperature, custom)
    condition         VARCHAR(4)   NOT NULL,   -- >, <, >=, <=
    threshold         DOUBLE PRECISION NOT NULL,
    duration          VARCHAR(16)  NOT NULL DEFAULT '5m',
    enabled           BOOLEAN      NOT NULL DEFAULT true,
    grafana_rule_uid  VARCHAR(64),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(monitoring_app_id, name)
);

CREATE INDEX IF NOT EXISTS idx_monitoring_alert_rules_app ON monitoring_alert_rules(monitoring_app_id);
