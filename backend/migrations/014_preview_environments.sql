-- 014_preview_environments.sql
-- Extends environments + project_quotas for preview (ephemeral PR) environments.
-- Forward-only, additive. Idempotent.

-- ① Extend environments with preview-env columns.
ALTER TABLE environments
    ADD COLUMN IF NOT EXISTS is_ephemeral    BOOLEAN      NOT NULL DEFAULT FALSE;

ALTER TABLE environments
    ADD COLUMN IF NOT EXISTS git_repo_id     UUID         REFERENCES git_repos(id) ON DELETE CASCADE;

ALTER TABLE environments
    ADD COLUMN IF NOT EXISTS pr_number       INTEGER;

ALTER TABLE environments
    ADD COLUMN IF NOT EXISTS pr_head_branch  VARCHAR(255);

ALTER TABLE environments
    ADD COLUMN IF NOT EXISTS parent_env_id   UUID         REFERENCES environments(id);

ALTER TABLE environments
    ADD COLUMN IF NOT EXISTS expires_at      TIMESTAMPTZ;

-- ② Relax environments.type CHECK to admit 'preview'.
-- The auto-generated constraint name is environments_type_check (confirmed on live DB).
-- Drop first so the ADD is idempotent on re-run.
ALTER TABLE environments
    DROP CONSTRAINT IF EXISTS environments_type_check;

ALTER TABLE environments
    ADD CONSTRAINT environments_type_check
    CHECK (type IN ('dev', 'prod', 'preview'));

-- ③ Unique index: one preview env per (git_repo, pr_number).
-- Prevents duplicate preview envs for the same PR.
CREATE UNIQUE INDEX IF NOT EXISTS idx_environments_preview_pr
    ON environments(git_repo_id, pr_number)
    WHERE is_ephemeral;

-- Index for the TTL reaper to find expired preview envs efficiently.
CREATE INDEX IF NOT EXISTS idx_environments_expires_at
    ON environments(expires_at)
    WHERE is_ephemeral;

-- ④ project_quotas: cap on concurrent preview envs per project.
ALTER TABLE project_quotas
    ADD COLUMN IF NOT EXISTS preview_env_max INTEGER NOT NULL DEFAULT 5;
