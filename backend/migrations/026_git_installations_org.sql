ALTER TABLE git_app_installations
    ADD COLUMN IF NOT EXISTS org_id TEXT;

UPDATE git_app_installations gai
   SET org_id = p.org_id
  FROM projects p
 WHERE p.id = gai.project_id
   AND gai.org_id IS NULL;

ALTER TABLE git_app_installations
    ALTER COLUMN org_id SET NOT NULL;

-- Collapsing every project into a single org (migration 022) leaves multiple
-- per-project installation rows that now share (org_id, provider, installation_id).
-- Before the new unique constraint can hold, dedup each group down to the oldest
-- row, repointing dependent git_repos to the survivor so no repo loses its link.
WITH ranked AS (
    SELECT id,
           first_value(id) OVER w AS keep_id,
           row_number()    OVER w AS rn
      FROM git_app_installations
    WINDOW w AS (
        PARTITION BY org_id, provider, installation_id
        ORDER BY created_at, id
    )
)
UPDATE git_repos r
   SET installation_id = ranked.keep_id
  FROM ranked
 WHERE r.installation_id = ranked.id
   AND ranked.rn > 1;

WITH ranked AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY org_id, provider, installation_id
               ORDER BY created_at, id
           ) AS rn
      FROM git_app_installations
)
DELETE FROM git_app_installations gai
 USING ranked
 WHERE gai.id = ranked.id
   AND ranked.rn > 1;

ALTER TABLE git_app_installations
    DROP CONSTRAINT IF EXISTS git_app_installations_project_id_provider_installation_id_key;

ALTER TABLE git_app_installations
    ADD CONSTRAINT git_app_installations_org_provider_installation_key
    UNIQUE (org_id, provider, installation_id);

CREATE INDEX IF NOT EXISTS idx_git_app_installations_org
    ON git_app_installations(org_id);
