-- 023_git_repos_app_spec.sql
-- Git-linked apps are now created by their FIRST successful build, not up-front.
-- A repo can be connected before any App exists; the build-agent materializes the
-- App (CreateApp) from the first real image. These columns carry the intended app
-- spec captured at connect time so that first CreateApp has port/replicas/profile.
-- Subsequent builds just DeployImageVersion onto the existing app.
--
-- Forward-only, idempotent.

ALTER TABLE git_repos
    ADD COLUMN IF NOT EXISTS port     INT         NOT NULL DEFAULT 8080,
    ADD COLUMN IF NOT EXISTS replicas INT         NOT NULL DEFAULT 2,
    ADD COLUMN IF NOT EXISTS profile  VARCHAR(20) NOT NULL DEFAULT 'small';
