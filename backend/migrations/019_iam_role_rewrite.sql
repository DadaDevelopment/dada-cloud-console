-- 019_iam_role_rewrite.sql
-- ADR-009: dada-cloud stops owning project roles; it becomes a pure claim-reading
-- resource plane. user-service owns org/project identity + membership.
--
-- Two changes:
--   1. Add projects.org_id (TEXT, nullable) — the org that owns the project.
--      user-service supplies it on POST /internal/projects. Authz cascades from
--      org_role onto every project sharing this org_id. TEXT (not UUID) because
--      the org id arrives as a string claim (org_id) and is compared verbatim;
--      keeping it TEXT avoids uuid<->text cast mismatches in the cascade queries.
--   2. Rewrite project_members.role from the legacy 4-role enum to the uniform
--      Owner/Admin/Developer/ReadOnly model. The table is DEMOTED — membership is
--      now sourced from fat JWT claims, not this table. Kept (not dropped) for
--      backfill/audit and any not-yet-migrated read path. No new writers.

ALTER TABLE projects ADD COLUMN IF NOT EXISTS org_id TEXT;
CREATE INDEX IF NOT EXISTS idx_projects_org_id ON projects(org_id);

-- 4-way legacy -> uniform map. Idempotent: rerunning is a no-op once values are
-- already in the new vocabulary (no legacy values left to match).
UPDATE project_members SET role = 'Owner'     WHERE role = 'platform-admin';
UPDATE project_members SET role = 'Admin'     WHERE role = 'client-admin';
UPDATE project_members SET role = 'Developer' WHERE role = 'developer';
UPDATE project_members SET role = 'ReadOnly'  WHERE role = 'client-viewer';

COMMENT ON TABLE project_members IS
  'DEMOTED (ADR-009): membership is read from fat JWT claims, not this table. '
  'user-service owns project membership. Kept for backfill/audit only — no new writers.';
COMMENT ON COLUMN projects.org_id IS
  'Owning org id (ADR-009). Supplied by user-service via POST /internal/projects. '
  'org_role authz cascades onto all projects sharing this value.';
