-- A build whose git_repos row is deleted while it is still in flight can
-- never run: build-agent loads the repo by builds.git_repo_id, and since
-- migration 116 made that FK ON DELETE SET NULL (so build history survives
-- the app's deletion), the surviving build carries git_repo_id = NULL. The
-- runner then dies with `load repo 00000000-...: no rows in result set`,
-- the failure classifies as platform_error ("our side"), and the self-heal
-- pass requeues it up to the attempt cap -- measured 2026-09-15: the freitorsk
-- app chirping-kolyaska burned attempts 1..6 over 2.5 hours on 2026-09-12
-- while its owner watched, pressed rebuild three times, ran auto-fix into a
-- PR he never saw, and deleted the app; the same zero-repo loop is visible
-- on instatic and nav before him. A repository that no longer exists is not
-- a transient platform fault: no amount of retrying can bring the row back,
-- so a NULL-repo row must not be requeued and any still in flight must be
-- failed with the repo_detached code the console already renders as
-- "our side, nothing to fix" (build-failure.ts needsRepoReconnect says no to
-- it on purpose).
--
-- Forward-only, idempotent: only non-terminal rows are closed, terminal rows
-- keep their original verdict. Rows whose git_repo_id is NULL and already
-- terminal exist legitimately (build history survived a delete) and must not
-- be rewritten.
UPDATE builds
   SET status = 'failed',
       fail_reason = 'repo_detached',
       error_message = 'repository was unlinked from this app; the build can no longer run',
       finished_at = COALESCE(finished_at, NOW()),
       updated_at = NOW()
 WHERE git_repo_id IS NULL
   AND status IN ('queued', 'detecting', 'building', 'pushing');
