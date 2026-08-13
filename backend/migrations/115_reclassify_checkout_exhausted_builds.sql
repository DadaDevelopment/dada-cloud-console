-- 115_reclassify_checkout_exhausted_builds.sql
-- On 2026-08-13 our Jenkins shared-library host was unreachable for ~2.5
-- hours. Every build that hit it died with "Maximum checkout retry attempts
-- reached, aborting" -- our failure, not the user's code. The build-agent
-- that ran during the outage did not yet know that signature, so it stamped
-- those rows fail_reason='build_failed', which reads as "your code is
-- broken" in the console and, worse, keeps them outside the automatic
-- re-queue pass added in b7ed4d69 (it matches fail_reason='platform_error'
-- only).
--
-- This relabels exactly the rows whose stored error is that signature. The
-- message is produced by Jenkins itself, never by user code, so the match is
-- unambiguous; the error_message prefix is rewritten to match the new reason
-- so the row does not carry two contradictory labels.
--
-- Consequence, stated plainly: after this runs, the re-queue pass sees these
-- rows and rebuilds the ones that are still the newest build for their app.
-- That is the intended repair -- the user's commit failed only because of us.
--
-- Forward-only, bounded to the outage signature, idempotent.

UPDATE builds
   SET fail_reason   = 'platform_error',
       error_message = 'platform_error: ' || regexp_replace(error_message, '^build_failed: ', '')
 WHERE status = 'failed'
   AND fail_reason IS DISTINCT FROM 'platform_error'
   AND error_message LIKE '%Maximum checkout retry attempts reached%';
