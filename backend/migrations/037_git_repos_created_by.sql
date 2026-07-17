-- 037_git_repos_created_by.sql
-- Records which human connected a git repo, so build-agent's first-build
-- CreateApp handoff can attribute the audit row to that user instead of the
-- fixed system actor when the triggering push itself has no user in the loop.

ALTER TABLE git_repos
    ADD COLUMN IF NOT EXISTS created_by UUID REFERENCES users(id) ON DELETE SET NULL;
