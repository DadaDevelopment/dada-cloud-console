-- 034_builds_attempt.sql
-- Track how many times a build has been dispatched so the build-agent can retry
-- a build that failed on a transient Jenkins/ingress error (503, timeout,
-- external ABORTED) instead of dead-ending it. Bounded by the agent
-- (maxBuildAttempts); the column starts at 1 for the first dispatch.

ALTER TABLE builds
    ADD COLUMN IF NOT EXISTS attempt INTEGER NOT NULL DEFAULT 1;
