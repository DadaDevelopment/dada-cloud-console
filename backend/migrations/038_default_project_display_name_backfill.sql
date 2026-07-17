-- 038_default_project_display_name_backfill.sql
-- Auto-provisioned personal projects (EnsureDefaultProject) all shipped with
-- display_name literal 'Default', so admin-facing lists (audit, projects)
-- showed dozens of rows named "Default" distinguishable only by slug.
-- Backfills existing rows to "<owner username>'s project"; new provisioning
-- already writes this pattern going forward (backend/internal/api/projects.go).

UPDATE projects p
SET display_name = u.username || '''s project'
FROM users u
WHERE p.owner_id = u.id
  AND p.display_name = 'Default';
