-- 124_audit_events_actor_type.sql
--
-- audit_events had no column telling a human actor from the platform's own
-- writes. Every reader that wanted "what did users do" had to remember, on its
-- own, to exclude actor_id = the seeded system uuid (010_system_user.sql, the
-- all-zero uuid writeAuditRow calls systemDeployActorID) -- a rule that lives
-- in nobody's schema and everybody's head. It bit for real: a SetDatabaseTier
-- row our own db_tier_reconciler wrote under that actor took first place in a
-- TERMINAL-action breakdown and read as a user abandoning a database-tier
-- change mid-flight.
--
-- actor_type is a closed vocabulary ('user' | 'system' | 'service'), set at
-- the point of insert (writeAuditRow and the handful of raw
-- INSERT INTO audit_events call sites), not derived later by a reader.
-- 'service' is reserved for a future machine-to-machine actor that is neither
-- a person nor the platform's own systemDeployActorID; nothing writes it yet.
--
-- Split across three files (124/125/126) because this repo's migration runner
-- (backend/internal/db/migrations.go) sends each file's full contents as one
-- query string, and Postgres treats a multi-statement string as a single
-- implicit transaction unless it contains explicit BEGIN/COMMIT -- and even
-- then, a CALL to a procedure that COMMITs internally is only legal when that
-- CALL is the sole statement in its own query string (verified against a local
-- Postgres 14: identical CALL fails with "invalid transaction termination"
-- when any other statement, even one separated by an explicit COMMIT,
-- precedes it in the same string). So the column/constraint/procedure setup
-- lives here, the batched backfill that actually needs per-batch commits gets
-- its own file (125) with nothing else in it, and the NOT NULL/cleanup lives
-- in a third (126) that runs after the backfill is known to be done.
--
-- Safety on a live table: ADD COLUMN with no DEFAULT is metadata-only (no
-- rewrite, no long lock, PG11+). The CHECK is added NOT VALID here so adding
-- it is also metadata-only; 126 VALIDATEs it once the backfill has run, which
-- takes SHARE UPDATE EXCLUSIVE (blocks other DDL, not reads or writes).

ALTER TABLE audit_events
    ADD COLUMN IF NOT EXISTS actor_type TEXT;

ALTER TABLE audit_events
    DROP CONSTRAINT IF EXISTS audit_events_actor_type_value_check;
ALTER TABLE audit_events
    ADD CONSTRAINT audit_events_actor_type_value_check
        CHECK (actor_type IN ('user', 'system', 'service')) NOT VALID;

-- audit_events_backfill_actor_type is called alone by 125.sql. It commits
-- after every batch on purpose: a single UPDATE across the whole table would
-- hold every touched row's lock, and the whole table's worth of WAL, in one
-- long-running transaction against a live table. Batches of 10000 rows,
-- keyed on the primary key so each batch is a cheap indexed lookup, keep any
-- one transaction short.
CREATE OR REPLACE PROCEDURE audit_events_backfill_actor_type()
LANGUAGE plpgsql AS $$
DECLARE
    affected integer;
BEGIN
    LOOP
        UPDATE audit_events
           SET actor_type = CASE
                               WHEN actor_id = '00000000-0000-0000-0000-000000000000'::uuid
                               THEN 'system'
                               ELSE 'user'
                             END
         WHERE id IN (
             SELECT id FROM audit_events WHERE actor_type IS NULL LIMIT 10000
         );
        GET DIAGNOSTICS affected = ROW_COUNT;
        COMMIT;
        EXIT WHEN affected = 0;
    END LOOP;
END;
$$;
