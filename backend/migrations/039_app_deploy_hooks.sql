-- 039_app_deploy_hooks.sql
-- Per-app deploy-hook tokens: a revocable bearer credential external CI
-- (GitHub Actions) presents to POST /api/v1/deploy instead of a Keycloak JWT,
-- to trigger the existing DeployImageVersion operation for one app. Only the
-- sha256 hash is stored; the plaintext token is shown once at creation time.

CREATE TABLE IF NOT EXISTS app_deploy_hooks (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id     UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_id UUID        NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    app_name       TEXT        NOT NULL,
    name           TEXT        NOT NULL DEFAULT '',
    token_hash     TEXT        NOT NULL UNIQUE,
    token_prefix   TEXT        NOT NULL,
    created_by     UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at   TIMESTAMPTZ,
    revoked_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_app_deploy_hooks_active
    ON app_deploy_hooks (project_id, environment_id, app_name)
    WHERE revoked_at IS NULL;
