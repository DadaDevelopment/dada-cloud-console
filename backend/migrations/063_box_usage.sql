-- 063_box_usage.sql
-- Dada Box, ФАЗА 7: the per-minute usage ledger, plus the funnel view that has
-- been waiting for it since 060.
--
-- (Numbering: 060 is box_leads, 061-062 are the box object and its grants, so the
-- ledger takes 063. The plan text calls it "060" — that number was taken by the
-- funnel before this file was written.)
--
-- ONE ROW PER BILLED MINUTE PER KIND, AND NOTHING ELSE. Three properties of this
-- table carry the whole product claim about price, and each one is a shape, not a
-- convention a future writer could forget:
--
--   1. PK (box_id, minute_start, kind) is the idempotency anchor. The meter is a
--      60s ticker that runs UNGUARDED on every backend replica
--      (internal/api/box_meter.go — advisory_lock.go's own comment says idempotent
--      loops do). A replayed tick, a pod that crashed mid-tick and two racing
--      replicas therefore all collapse onto one row. Without the PK the
--      alternative is an advisory lock, and a lock means a single replica outage
--      is silent revenue loss.
--
--   2. AN IDLE MINUTE WRITES NO ROW AT ALL. Not a row with cost_rub = 0. The
--      ABSENCE of the row is the "not billed" statement, and it is the only form
--      of that statement a later pricing change cannot quietly reverse: a
--      zero-cost row is a row someone can start charging for by editing a YAML
--      file, whereas a row that does not exist has to be created deliberately by
--      code someone has to write and review. dada_box_idle_minutes_total counts
--      the idle minutes instead, so "idle is not billed" stays queryable without
--      being storable.
--
--   3. cost_rub IS FROZEN AT WRITE TIME. The price is derived, not looked up
--      (costengine.PerMinuteCost, decision D5 — no price table), so the derivation
--      inputs move whenever box-fleet-cost.yaml does. Recomputing history from
--      today's inputs would silently re-price last month's invoice. The footprint
--      the price came from is stored alongside it so any row can be re-derived and
--      audited without guessing which catalog entry was live at the time.

-- ---------------------------------------------------------------------------
-- box_usage
-- ---------------------------------------------------------------------------
--
-- box_id is a BARE UUID, deliberately not a foreign key to boxes(id), for the
-- same reason box_grants.box_id is not one (060) plus a stronger one: this is a
-- billing ledger. boxes rows are tombstoned rather than deleted, but a project
-- deletion cascades to environments and on to boxes, and an ON DELETE CASCADE
-- here would erase the record of what a customer consumed at the moment their
-- project was removed. A ledger that disappears with its subject cannot settle a
-- dispute about a bill. Orphan rows are the acceptable side of that trade.
--
-- kind carries NO CHECK constraint, and that is a decision rather than an
-- omission. The meter writes exactly two values ('active', 'suspended_disk' —
-- boxUsageKind* in internal/api/box_meter.go) and is the only writer in the
-- backend, but the funnel's pinned definition test
-- (TestCollectBoxRepeatUsePublishesTheMeasuredRatio, which this migration is what
-- un-skips) seeds kind='cpu'. A CHECK would either fail that test or would have to
-- enumerate a value the meter never writes; the ledger genuinely does not care,
-- and the view below is explicit about which kinds mean activity.
CREATE TABLE IF NOT EXISTS box_usage (
    box_id       UUID        NOT NULL,
    minute_start TIMESTAMPTZ NOT NULL,
    kind         TEXT        NOT NULL,

    -- Tenancy, denormalized ON PURPOSE. The row must remain attributable after
    -- the box, the environment and even the project are gone (see the no-FK note
    -- above), so it carries its own answer to "whose minute was this" instead of
    -- a join that will one day return nothing. org_id is TEXT to match
    -- billing_accounts.org_id / projects.org_id.
    org_id     TEXT NOT NULL DEFAULT '',
    project_id UUID,

    -- The footprint this minute was priced from, and the price. Frozen: see
    -- property 3 in the header. NUMERIC, never float — this column is summed
    -- against spend_cap_rub and binary floating point accumulates a drift that
    -- would make a cap fire at 999.9999 or 1000.0001 depending on row order.
    vcpu       NUMERIC(8,3)  NOT NULL DEFAULT 0,
    ram_gb     NUMERIC(8,3)  NOT NULL DEFAULT 0,
    storage_gb NUMERIC(8,3)  NOT NULL DEFAULT 0,
    -- Six decimals because one active minute of a standard box is a fraction of a
    -- kopeck. Rounding to 2 here would round most rows to zero and the ledger
    -- would sum to nothing.
    cost_rub   NUMERIC(14,6) NOT NULL DEFAULT 0,

    -- When the meter wrote the row, as distinct from the minute it describes. The
    -- gap between them is dada_box_metered_minutes_lag_seconds, i.e. how far
    -- behind the meter is running.
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (box_id, minute_start, kind)
);

-- Serves the spend-cap sum (per box, whole month) and getBoxUsage's window
-- query. The PK already leads with box_id, so this index exists for the
-- month-slice scan the cap check does on every tick.
CREATE INDEX IF NOT EXISTS idx_box_usage_box_minute
    ON box_usage (box_id, minute_start DESC);

-- Serves the org rollup in billing_meter.go (box_minutes into usage_records) and
-- the per-project rollup in consumptionBoxes.
CREATE INDEX IF NOT EXISTS idx_box_usage_org_minute
    ON box_usage (org_id, minute_start DESC);
CREATE INDEX IF NOT EXISTS idx_box_usage_project_minute
    ON box_usage (project_id, minute_start DESC)
    WHERE project_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- boxes: the three columns the cap and the reaper need.
-- ---------------------------------------------------------------------------
--
-- spend_capped_at is the irreversibility marker. Reaching the cap suspends the
-- box (never deletes it — the data survives the customer's own runaway), and this
-- timestamp is what stops the reaper from re-enqueueing a suspend every 60s. It
-- is deliberately NOT cleared by ResumeBox: a customer who resumes a capped box
-- without raising the cap would be capped again within the minute, which reads as
-- a broken product rather than as an enforced limit. Clearing it requires raising
-- spend_cap_rub, i.e. an explicit decision, which is what "irreversible without
-- an operator action" means here.
--
-- last_sample_active is the AUTHORITATIVE billing verdict, taken outside the
-- guest by our agent. It was previously implicit in last_active_at, which is a
-- recency stamp several writers touch; billing needs the boolean itself, because
-- "the last sample said idle" and "no sample has arrived" must be
-- distinguishable and both must be distinguishable from "something else bumped
-- the box".
--
-- guest_heartbeat_at is the IN-GUEST claim, and the asymmetry between it and
-- last_sample_active is a security property, not a nicety. A box runs as root
-- under the customer's own agent. If the in-guest signal were authoritative, a
-- customer could under-report their own usage and anyone could accuse us of
-- over-reporting. So the in-guest heartbeat may only ask for MORE billing (it
-- defers suspension and can mark a minute active) and can never mark a minute
-- idle. Trusting it is safe precisely because it can only cost the guest money.
-- spend_cap_warned_at / spend_cap_delete_warned_at are the "send this email at
-- most once" stamps. They live on the row rather than in the process because the
-- meter runs on EVERY replica: a boolean held in memory would send one email per
-- pod, and a conditional UPDATE against a NULL column is what makes exactly one
-- replica the sender.
ALTER TABLE boxes ADD COLUMN IF NOT EXISTS spend_capped_at     TIMESTAMPTZ;
ALTER TABLE boxes ADD COLUMN IF NOT EXISTS spend_cap_warned_at TIMESTAMPTZ;
ALTER TABLE boxes ADD COLUMN IF NOT EXISTS spend_cap_delete_warned_at TIMESTAMPTZ;
ALTER TABLE boxes ADD COLUMN IF NOT EXISTS last_sample_active  BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE boxes ADD COLUMN IF NOT EXISTS guest_heartbeat_at  TIMESTAMPTZ;

-- Reap-warning bookkeeping. The 72h-asleep reaper sends TWO emails before it
-- destroys anything, and those stamps are what make "two emails, then delete"
-- true across pod restarts and across replicas instead of true only within one
-- process's memory.
ALTER TABLE boxes ADD COLUMN IF NOT EXISTS reap_warned_at      TIMESTAMPTZ;
ALTER TABLE boxes ADD COLUMN IF NOT EXISTS reap_final_warned_at TIMESTAMPTZ;

-- ---------------------------------------------------------------------------
-- box_repeat_use_7d — re-running the gated block from 060_box_leads.sql.
-- ---------------------------------------------------------------------------
--
-- THIS IS THE POINT OF DOING IT HERE. 060 creates the view only when
-- to_regclass('public.box_usage') is non-null, so every database that migrated
-- past 060 before today skipped it and the brief's HEADLINE METRIC
-- (dada_box_repeat_use_7d_ratio) reports NaN there forever — a permanently dark
-- number, on every existing environment, with no error anywhere to notice.
-- CREATE OR REPLACE VIEW makes re-running it idempotent, so a fresh database that
-- runs 060 after this file exists gets the same view twice and is fine.
--
-- ONE DELIBERATE DIFFERENCE FROM 060's TEXT, and it is a semantic one that has to
-- be stated out loud rather than slipped in:
--
--   060 wrote `JOIN box_usage u ON u.box_id = g.box_id` with a comment that it
--   "collapses the kind dimension so a minute billed under two kinds is still one
--   minute of activity". That was written when every row in the ledger was
--   expected to mean activity. It no longer is: kind='suspended_disk' is a row for
--   a minute in which the box was ASLEEP, billed only because a 40 GiB rootfs
--   still sits on our storage. Counting those as activity would be actively
--   destructive to this exact metric — 72 hours of contiguous sleeping minutes
--   would merge every session on that box into ONE session (the 30-minute gap rule
--   never fires), so sessions_within_7d collapses to 1 and repeat_use becomes
--   false for the very users who came back. The headline number would read
--   "nobody returned" for a mechanical reason having nothing to do with people.
--
--   Hence the WHERE below. It is written as an exclusion rather than
--   `kind = 'active'` so the funnel's own pinned test (which seeds kind='cpu')
--   keeps measuring what it was written to measure. Any future kind that does NOT
--   mean "the customer was working" must be added to this exclusion list.
DO $$
BEGIN
  IF to_regclass('public.box_usage') IS NULL THEN
    RAISE NOTICE 'box_repeat_use_7d NOT created: box_usage is missing, which should be impossible in this migration.';
    RETURN;
  END IF;

  EXECUTE $view$
    CREATE OR REPLACE VIEW box_repeat_use_7d AS
    -- One row per (claim, box, active minute). Collapses the remaining `kind`
    -- values so a minute billed under two of them is still one minute of activity;
    -- storage-only accrual is excluded above the fold, see the note above.
    WITH active_minutes AS (
        SELECT g.claim, u.box_id, u.minute_start
          FROM box_grants g
          JOIN box_usage  u ON u.box_id = g.box_id
         WHERE u.kind <> 'suspended_disk'
         GROUP BY g.claim, u.box_id, u.minute_start
    ),
    -- Session boundary: strictly more than 30 minutes between consecutive active
    -- minute_starts of the SAME box starts a new session.
    boundaries AS (
        SELECT claim, box_id, minute_start,
               CASE
                 WHEN LAG(minute_start) OVER w IS NULL
                   OR minute_start - LAG(minute_start) OVER w > interval '30 minutes'
                 THEN 1 ELSE 0
               END AS is_new_session
          FROM active_minutes
        WINDOW w AS (PARTITION BY box_id ORDER BY minute_start)
    ),
    numbered AS (
        SELECT claim, box_id, minute_start,
               SUM(is_new_session) OVER (PARTITION BY box_id ORDER BY minute_start) AS session_no
          FROM boundaries
    ),
    session_starts AS (
        SELECT claim, box_id, session_no, MIN(minute_start) AS started_at
          FROM numbered
         GROUP BY claim, box_id, session_no
    ),
    -- First activation is per CLAIM, not per box: a claim granted two boxes is
    -- still one person, and their second session may live on the other box.
    first_activation AS (
        SELECT claim, MIN(started_at) AS first_at
          FROM session_starts
         GROUP BY claim
    ),
    within_7d AS (
        SELECT s.claim, s.started_at, f.first_at
          FROM session_starts s
          JOIN first_activation f USING (claim)
         WHERE s.started_at <= f.first_at + interval '7 days'
    )
    SELECT claim,
           MIN(first_at)                                   AS first_activation_at,
           COUNT(*)                                        AS sessions_within_7d,
           MAX(started_at)                                 AS last_session_start_within_7d,
           -- The first session starts exactly at first_at, so "some pair of
           -- session starts is >= 24h apart" reduces to "the latest start in the
           -- window is >= 24h after the first".
           BOOL_OR(started_at >= first_at + interval '24 hours') AS repeat_use
      FROM within_7d
     GROUP BY claim
  $view$;

  EXECUTE 'GRANT SELECT ON box_repeat_use_7d TO dada';
END
$$;

-- Explicit grants, named per table. 033_regrant_dada_all_tables.sql records why
-- default privileges cannot be trusted: a table created by a role whose default
-- privileges do not include dada silently lacks access, and the symptom surfaces
-- far from the cause (that is how 030_default_domains broke the build-agent's
-- deploy handoff). box_usage has no sequence — the PK is natural — so there is
-- nothing to grant on one.
GRANT SELECT, INSERT, UPDATE, DELETE ON box_usage TO dada;
