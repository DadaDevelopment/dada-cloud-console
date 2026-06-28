ALTER TABLE git_app_installations
    ADD COLUMN IF NOT EXISTS org_id TEXT;

UPDATE git_app_installations gai
   SET org_id = p.org_id
  FROM projects p
 WHERE p.id = gai.project_id
   AND gai.org_id IS NULL;

ALTER TABLE git_app_installations
    ALTER COLUMN org_id SET NOT NULL;

ALTER TABLE git_app_installations
    DROP CONSTRAINT IF EXISTS git_app_installations_project_id_provider_installation_id_key;

ALTER TABLE git_app_installations
    ADD CONSTRAINT git_app_installations_org_provider_installation_key
    UNIQUE (org_id, provider, installation_id);

CREATE INDEX IF NOT EXISTS idx_git_app_installations_org
    ON git_app_installations(org_id);
