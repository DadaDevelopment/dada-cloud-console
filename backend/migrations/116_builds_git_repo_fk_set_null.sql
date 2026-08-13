-- 116_builds_git_repo_fk_set_null.sql
-- builds.git_repo_id has referenced git_repos(id) ON DELETE CASCADE since 001,
-- so every path that drops a git_repos row also destroys that app's entire
-- build history: DisconnectGitRepo, DeleteApp, and a re-upload that replaces
-- the source row. The user loses the record of why their earlier attempts
-- failed, and every funnel number computed over `builds` is biased toward
-- survivors -- measured 2026-08-13: of 26 build ids named by BuildFinished
-- audit rows in the preceding 48h, only 6 still had a row in `builds`, i.e.
-- 77% of the denominator had been erased retroactively.
--
-- SET NULL for the same reason 044, 093 and 110 chose it: a build is a record
-- of something that happened, and it must outlive the source row it happened
-- from. app_name, branch, commit_sha, status, fail_reason, image_uri and
-- environment_id all live on the build row itself, so a detached build is
-- still readable and still attributable to an app.
--
-- environment_id keeps its CASCADE on purpose: when the environment is gone
-- the app is gone, and a build with no environment has no page to appear on.
--
-- Forward-only, idempotent (constraint name is the stable default from 001).

ALTER TABLE builds
    ALTER COLUMN git_repo_id DROP NOT NULL;

ALTER TABLE builds
    DROP CONSTRAINT IF EXISTS builds_git_repo_id_fkey,
    ADD CONSTRAINT builds_git_repo_id_fkey
        FOREIGN KEY (git_repo_id) REFERENCES git_repos(id) ON DELETE SET NULL;
