ALTER TABLE builds ADD COLUMN IF NOT EXISTS gh_deployment_id BIGINT;
ALTER TABLE builds ADD COLUMN IF NOT EXISTS gh_installation_id BIGINT;
ALTER TABLE builds ADD COLUMN IF NOT EXISTS gh_deployment_state VARCHAR(20);

CREATE INDEX IF NOT EXISTS idx_builds_gh_deployment_open
    ON builds(created_at)
    WHERE gh_deployment_state = 'in_progress';
