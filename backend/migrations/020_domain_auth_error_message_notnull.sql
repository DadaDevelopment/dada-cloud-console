-- 020_domain_auth_error_message_notnull.sql
-- Fix: domain_authorizations.error_message was nullable with no default, and the
-- INSERT in AddDomainAuthorization never sets it, so every fresh row had NULL.
-- The Go model scans error_message into a non-nullable `string`, so the background
-- DNS poller (VerifyPendingDomains) — and the List/Verify read paths — aborted with
-- "cannot scan NULL into *string" on any such row, meaning no domain ever
-- auto-verified. Backfill existing NULLs and make the column NOT NULL DEFAULT ''.
-- Forward-only, additive, idempotent.

UPDATE domain_authorizations SET error_message = '' WHERE error_message IS NULL;

ALTER TABLE domain_authorizations ALTER COLUMN error_message SET DEFAULT '';
ALTER TABLE domain_authorizations ALTER COLUMN error_message SET NOT NULL;
