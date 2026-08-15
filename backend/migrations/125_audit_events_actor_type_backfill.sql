-- 125_audit_events_actor_type_backfill.sql
--
-- Runs the batched backfill procedure created by 124.sql. This file must
-- contain exactly this one statement and nothing else: a CALL to a procedure
-- that COMMITs internally is only legal when it is the sole statement in its
-- own query string (see 124.sql's header for how that was verified). Adding a
-- second statement here -- even one meant to run after it -- would break the
-- per-batch commits and turn this back into the single long transaction the
-- split was meant to avoid.
CALL audit_events_backfill_actor_type();
