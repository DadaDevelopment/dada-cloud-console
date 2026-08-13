-- 114_users_signup_attribution.sql
-- Every new registration must record where it came from. Before this,
-- users carried no attribution columns at all, so a signup's channel
-- (utm_source/medium/campaign, referrer) was unrecoverable the moment the
-- session ended -- there is nothing to backfill; the history genuinely does
-- not exist for any row created before this migration.
--
-- Columns are nullable and written once, at signup time, inside the same
-- upsert statement that creates the users row (see auth.ResolveUser) --
-- first touch wins, never overwritten on a later login.
--
-- Forward-only, additive.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS signup_source   TEXT,
    ADD COLUMN IF NOT EXISTS signup_medium   TEXT,
    ADD COLUMN IF NOT EXISTS signup_campaign TEXT,
    ADD COLUMN IF NOT EXISTS signup_referrer TEXT;
