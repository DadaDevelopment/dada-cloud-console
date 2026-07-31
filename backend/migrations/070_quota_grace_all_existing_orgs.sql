-- Grandfathering, honestly: every org that already had resources when quota
-- enforcement went live gets the same announced grace window, not only the
-- orgs that were strictly over the free quota.
--
-- Migration 055 backfilled quota_grace_until only for orgs with apps > 2 or
-- dbs > 1 or domains > 1. An org sitting exactly AT the free ceiling (2 apps,
-- 1 db) got no billing_accounts row at all, and quotaGraceActive treats a
-- missing row as "no grace" -- so those users hit a hard 403 on their next
-- app or database with no warning and no grace, while a heavier neighbour
-- kept working. Same promise, opposite treatment, decided by an accident of
-- the threshold.
--
-- The grace timestamp is copied from whatever 055 already assigned rather
-- than recomputed, so every grandfathered org shares ONE announced cutoff
-- date that can be named in an email. Existing rows are never touched:
-- ON CONFLICT DO NOTHING keeps paid plans and any manually adjusted grace
-- intact.

INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, quota_grace_until, created_at, updated_at)
SELECT DISTINCT p.org_id,
       'free',
       now(),
       coalesce(
         (SELECT max(quota_grace_until) FROM billing_accounts WHERE quota_grace_until IS NOT NULL),
         now() + interval '60 days'
       ),
       now(),
       now()
FROM resource_snapshots rs
JOIN projects p ON p.id = rs.project_id
WHERE p.org_id IS NOT NULL
  AND p.org_id <> ''
  AND rs.kind IN ('App', 'ServiceDatabase', 'ServiceDatabaseV2')
ON CONFLICT (org_id) DO NOTHING;
