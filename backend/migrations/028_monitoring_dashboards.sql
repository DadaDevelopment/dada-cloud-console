-- 028_monitoring_dashboards.sql
-- Server-side persistence for the ECharts observability dashboard. Today the
-- dashboard config (range, refresh, filters, group-by, aggregation, panel
-- layout + thresholds/annotations) lives only in the browser's localStorage,
-- so it does not follow the user across devices and is lost on cache clear.
--
-- One row per (monitoring_app, user): dashboards are PER-USER by default — each
-- user arranges their own view of a resource. The whole config is stored as an
-- opaque JSONB blob owned by the frontend (DashboardState), versioned so the
-- client can migrate stale shapes. user_id is not FK'd: users are owned by the
-- external IAM (user-service), like audit_events.actor_id.
-- Forward-only, additive, idempotent.

CREATE TABLE IF NOT EXISTS monitoring_dashboards (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    monitoring_app_id UUID        NOT NULL REFERENCES monitoring_apps(id) ON DELETE CASCADE,
    user_id           UUID        NOT NULL,
    config            JSONB       NOT NULL,
    version           INT         NOT NULL DEFAULT 1,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(monitoring_app_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_monitoring_dashboards_user
    ON monitoring_dashboards(user_id, monitoring_app_id);
