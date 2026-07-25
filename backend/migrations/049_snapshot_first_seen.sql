-- resource_snapshots.first_seen_at: immutable timestamp of a resource's FIRST
-- insert. No writer lists this column in its ON CONFLICT ... DO UPDATE SET, so
-- once a row exists the value is frozen (LWW reconcile ticks never touch it) --
-- the agents need no code change for this to hold.
--
-- Fixes the admin "Новые приложения в день" chart, which proxied creation time
-- with min(last_synced_at). last_synced_at is re-stamped every ~30s status
-- reconcile (gitops-agent UpdateLiveStatus), so every currently-live app's
-- "first seen" collapsed onto the current day -- the persistent today-spike.
--
-- Backfill existing rows from the earliest build per (environment_id, app_name),
-- the strongest historical first-deploy signal we have. Apps never built keep
-- the migration-time default; that is a one-time artifact that scrolls out of
-- the (<=90d) dynamics window and never recurs, since new rows get a true
-- first_seen_at at insert.

ALTER TABLE resource_snapshots
    ADD COLUMN IF NOT EXISTS first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now();

UPDATE resource_snapshots rs
SET first_seen_at = b.first_build
FROM (
    SELECT environment_id, app_name, min(created_at) AS first_build
    FROM builds
    GROUP BY environment_id, app_name
) b
WHERE rs.kind = 'App'
  AND rs.environment_id = b.environment_id
  AND rs.name = b.app_name
  AND b.first_build < rs.first_seen_at;

CREATE INDEX IF NOT EXISTS idx_resource_snapshots_first_seen
    ON resource_snapshots (kind, first_seen_at);
