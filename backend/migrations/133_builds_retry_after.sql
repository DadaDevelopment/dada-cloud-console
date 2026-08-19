-- A build re-queued after a transient Jenkins failure used to be claimable the
-- instant it was written, so all three attempts burned inside a few seconds of
-- one outage and the user was left with a red build for a fault that cleared a
-- minute later. retry_after holds the row back until the outage has had time
-- to end.
--
-- Nullable with no default on purpose: every row written by a replica running
-- the previous image leaves it NULL, which reads as "claimable now" and keeps
-- the old and new agent behaving identically during a rolling update.
ALTER TABLE builds ADD COLUMN IF NOT EXISTS retry_after TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_builds_queued_retry_after
    ON builds (retry_after)
    WHERE status = 'queued';
