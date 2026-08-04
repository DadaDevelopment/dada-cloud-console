-- Demo apps deployed from a platform starter template are a showroom, not the
-- user's work: the console builds them from our own repository so somebody with
-- no code of their own can still see a deploy happen. They were never cleaned
-- up, so they idled forever -- one nextjs-jvuu2y sat Ready for eighteen days in
-- a project whose owner never deployed anything of their own, burning cluster
-- capacity nobody was ever going to pay for.
--
-- demo_expires_at is the reaper's deadline: stamped at link time for starter
-- repos only, cleared the moment the user claims the app ("keep") or the reaper
-- enqueues the delete. NULL means "not a demo, or no longer one", which is the
-- state every ordinary GitHub/upload app stays in forever.
ALTER TABLE git_repos
    ADD COLUMN IF NOT EXISTS demo_expires_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_git_repos_demo_expires_at
    ON git_repos (demo_expires_at)
    WHERE demo_expires_at IS NOT NULL;
