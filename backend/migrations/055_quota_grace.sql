ALTER TABLE billing_accounts ADD COLUMN IF NOT EXISTS quota_grace_until TIMESTAMPTZ;

WITH resource_usage AS (
  SELECT p.org_id,
         count(*) FILTER (WHERE rs.kind = 'App')                                        AS apps,
         count(*) FILTER (WHERE rs.kind IN ('ServiceDatabase', 'ServiceDatabaseV2'))    AS dbs
  FROM resource_snapshots rs
  JOIN projects p ON p.id = rs.project_id
  WHERE p.org_id IS NOT NULL AND p.org_id <> ''
  GROUP BY p.org_id
),
domain_usage AS (
  SELECT p.org_id, count(*) AS domains
  FROM domain_authorizations da
  JOIN projects p ON p.id = da.project_id
  WHERE p.org_id IS NOT NULL AND p.org_id <> ''
  GROUP BY p.org_id
),
over_free AS (
  SELECT org_id FROM resource_usage WHERE apps > 2 OR dbs > 1
  UNION
  SELECT org_id FROM domain_usage WHERE domains > 1
)
INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, quota_grace_until, created_at, updated_at)
SELECT org_id, 'free', now(), now() + interval '60 days', now(), now()
FROM over_free
ON CONFLICT (org_id) DO UPDATE
  SET quota_grace_until = EXCLUDED.quota_grace_until,
      updated_at        = EXCLUDED.updated_at;
