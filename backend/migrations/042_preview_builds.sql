-- 042_preview_builds.sql
-- Preview deployments (build-agent side): a build row triggered by a pull
-- request needs to carry the PR number (for status posting + preview-env
-- lookup on close) and a fork-safety flag (never inject clone secrets for a
-- build coming from a fork). Both are additive and nullable/defaulted, so
-- existing push/manual/rollback builds are unaffected.
-- Forward-only, additive. Idempotent (matches the IF NOT EXISTS pattern used
-- since 014_preview_environments.sql).

ALTER TABLE builds
    ADD COLUMN IF NOT EXISTS pr_number INTEGER;

ALTER TABLE builds
    ADD COLUMN IF NOT EXISTS fork_unsafe BOOLEAN NOT NULL DEFAULT FALSE;
