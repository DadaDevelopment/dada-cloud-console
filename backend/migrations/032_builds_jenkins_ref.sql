-- 032_builds_jenkins_ref.sql
-- Persist the Jenkins queue item id and build number on each build so the
-- build-agent can re-attach to a still-running Jenkins job after a restart
-- (redeploy/OOM/evict) instead of losing the build. The Jenkins job keeps
-- running when the agent pod dies; without these the agent has no way to map a
-- non-terminal builds row back to its running job to resume streaming and
-- finalize (success + deploy handoff). Both nullable: they are only known once
-- the job is triggered, and older/never-triggered builds keep them NULL.

ALTER TABLE builds
    ADD COLUMN IF NOT EXISTS jenkins_queue_id BIGINT;

ALTER TABLE builds
    ADD COLUMN IF NOT EXISTS jenkins_build_number INTEGER;
