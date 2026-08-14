-- Grace window for databases that are already over their storage quota the
-- first time a tier is applied to them. Enforcement on such a database is not a
-- reaction to growth the owner could have seen coming: the limit appeared under
-- data that was already there. grace_until is the deadline after which the
-- normal ladder applies; NULL means no grace has been granted (or it was
-- cleared when the database dropped back under the release threshold).
ALTER TABLE db_quota_state ADD COLUMN IF NOT EXISTS grace_until TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_db_quota_state_grace
    ON db_quota_state (grace_until)
    WHERE grace_until IS NOT NULL;
