-- 126_audit_events_actor_type_not_null.sql
--
-- Closes out the actor_type rollout started in 124/125: by the time this
-- runs, 125.sql's batched backfill has already set actor_type on every row,
-- so validating the enum CHECK and adding NOT NULL are both cheap. The NOT
-- NULL is proven via a second NOT VALID + VALIDATE CHECK pair rather than
-- ALTER COLUMN ... SET NOT NULL directly, so Postgres (12+) can skip
-- re-scanning the table for that guarantee -- it already knows every row
-- passed the validated CHECK (actor_type IS NOT NULL).
--
-- ORDER MATTERS, AND NOT FOR STYLE. Migrations run at the new pod's startup
-- while the old pods are still serving and still writing audit rows with no
-- actor_type -- that is what a rolling update is. Rows born in that window
-- land between 125's backfill and this file's NOT NULL. If the NOT NULL were
-- proven first, one audit row written by an old replica mid-rollout would
-- fail the validation, fail the migration, and crashloop the new backend
-- on a table nobody was even reading yet.
--
-- So the default is set BEFORE the closing sweep, and the sweep runs before
-- anything is validated:
--   SET DEFAULT closes the window going forward (it rewrites no existing row,
--   unlike ADD COLUMN ... DEFAULT, which is exactly why the default could not
--   live in 124: there it would have stamped every historical row 'system' and
--   erased the human/system split this change exists to record);
--   the sweep catches whatever an old replica wrote between 125 and now;
--   only then is there nothing left for VALIDATE to trip on.
--
-- DEFAULT 'system' is a fail-safe, not a rule any writer should rely on: every
-- writer sets actor_type explicitly at insert time (see 124.sql's header). If
-- one is ever added that forgets to, defaulting to 'system' means the row
-- disappears from user-action counts instead of silently posing as one --
-- the same failure mode this whole change exists to close, applied to itself.

ALTER TABLE audit_events
    ALTER COLUMN actor_type SET DEFAULT 'system';

UPDATE audit_events
   SET actor_type = CASE
                       WHEN actor_id = '00000000-0000-0000-0000-000000000000'::uuid
                       THEN 'system'
                       ELSE 'user'
                     END
 WHERE actor_type IS NULL;

ALTER TABLE audit_events VALIDATE CONSTRAINT audit_events_actor_type_value_check;

ALTER TABLE audit_events
    DROP CONSTRAINT IF EXISTS audit_events_actor_type_not_null_check;
ALTER TABLE audit_events
    ADD CONSTRAINT audit_events_actor_type_not_null_check
        CHECK (actor_type IS NOT NULL) NOT VALID;
ALTER TABLE audit_events VALIDATE CONSTRAINT audit_events_actor_type_not_null_check;

ALTER TABLE audit_events
    ALTER COLUMN actor_type SET NOT NULL;
ALTER TABLE audit_events
    DROP CONSTRAINT IF EXISTS audit_events_actor_type_not_null_check;

DROP PROCEDURE IF EXISTS audit_events_backfill_actor_type();
