-- Reminder bookkeeping for the grandfathering window.
--
-- expiry_notified_at already exists but belongs to the paid-plan term: a
-- renewal resets it, and reusing it would make a grace reminder cancel a
-- renewal reminder (and the reverse). Two independent lifecycles, two
-- columns.

ALTER TABLE billing_accounts ADD COLUMN IF NOT EXISTS grace_notified_at TIMESTAMPTZ;
