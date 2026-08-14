-- Per-build archive source, so uploading a folder stops unbinding the app from git.
--
-- Until now an upload rewrote the app's single git_repos row: provider became
-- 'archive', repo_full_name became 'upload/<app>', installation_id was nulled
-- and production_branch became 'upload'. That is destructive and silent. A user
-- who uploads once - or whose ddc deploy falls back to an archive for a few
-- transient seconds - loses the GitHub binding for good: pushes still arrive at
-- the webhook, ResolveReposByFullName finds nothing, and the delivery is dropped
-- without a line in the log. Owner hit exactly this on keksmd/family-tree
-- (2026-08-14 13:22 and 13:36 UTC: two pushes, two webhooks, zero builds).
--
-- Holding the archive on the build makes an upload what it reads like: one
-- build from these files, leaving the app's source binding alone.
ALTER TABLE builds
    ADD COLUMN IF NOT EXISTS archive_url       TEXT,
    ADD COLUMN IF NOT EXISTS archive_framework TEXT,
    ADD COLUMN IF NOT EXISTS archive_port      INTEGER;
