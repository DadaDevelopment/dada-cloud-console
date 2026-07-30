-- 060_box_leads.sql
-- Dada Box fake-door funnel storage.
--
-- Why a table and not the log line it replaces: the /box door used to write
-- `console.log("box_funnel " + JSON.stringify(record))`, which cannot answer the
-- questions the experiment exists for (docs/plans/2026-07-29-box-test-and-measurement.md §6):
--
--   1. No denominator. Page views lived in Yandex Metrika, events in a log line,
--      so view->request conversion was a ratio between two systems with no join key.
--   2. No identity. Every counter was a counter of events, not of people: one
--      curious visitor replaying the demo six times looked like six people.
--   3. Repeat use was structurally unmeasurable. It is a property of a granted
--      box, not of the landing (see box_grants below).
--
-- Modelled on the deliberately tiny 040_feedback.sql: four narrow relations, no
-- ORM, read over psql by whoever is running the experiment.
--
-- PII rules (152-ФЗ), enforced by the shape of these tables:
--   * The ONLY personal datum stored is the email/contact the person deliberately
--     typed into the form, and it lives in box_leads only.
--   * `vid` is the opaque first-party visitor id (dada_vid cookie) — a UUID and
--     nothing else. The API rejects any non-UUID value, so an email cannot be
--     smuggled into this column by a tampering client.
--   * There is no user_agent column on purpose. A UA string is fingerprintable
--     and adds nothing to the four funnel metrics; it stays in the fallback log
--     line and is never joined to an identity here.

-- ---------------------------------------------------------------------------
-- box_leads — one row per door submission (event = box_requested).
-- ---------------------------------------------------------------------------
-- `claim` is the human-readable request code (BOX-XXXX-XXXX) the person is shown
-- on the page. It is minted by the Next route handler, NOT here and NOT by the
-- backend: the door must keep working when this storage hop is down, so the code
-- the visitor sees can never depend on a successful INSERT (fail-open).
CREATE TABLE IF NOT EXISTS box_leads (
    claim       TEXT        PRIMARY KEY,
    email       TEXT        NOT NULL,
    contact     TEXT,
    agent       TEXT,
    parallel    TEXT,
    price       TEXT,
    use_case    TEXT        NOT NULL DEFAULT '',
    locale      TEXT        NOT NULL DEFAULT 'ru',
    utm_source  TEXT,
    vid         UUID,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_box_leads_created_at ON box_leads (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_box_leads_utm_source ON box_leads (utm_source, created_at DESC);

-- ---------------------------------------------------------------------------
-- box_funnel_events — every funnel event, including the server-side page_view
-- that gives the conversion ratio a denominator in the same store as its numerator.
-- ---------------------------------------------------------------------------
-- `claim` is deliberately NOT a foreign key to box_leads. crystallize_intent is
-- the highest-signal event in the whole experiment; losing one because the
-- earlier box_requested INSERT failed while the backend was down would trade the
-- most valuable measurement for referential tidiness.
--
-- `props` carries the small event-specific payload (crystallize_intent.wants).
-- It must never carry an email or free-text the person did not type into the form.
CREATE TABLE IF NOT EXISTS box_funnel_events (
    id         BIGSERIAL   PRIMARY KEY,
    at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    event      TEXT        NOT NULL,
    claim      TEXT,
    vid        UUID,
    locale     TEXT        NOT NULL DEFAULT 'ru',
    utm_source TEXT,
    referer    TEXT,
    props      JSONB       NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_box_funnel_events_at ON box_funnel_events (at DESC);
CREATE INDEX IF NOT EXISTS idx_box_funnel_events_event_at ON box_funnel_events (event, at DESC);
-- Serves the page_view session dedup lookup (vid + event + recency) and the
-- "distinct people, not distinct events" counts in the runbook.
CREATE INDEX IF NOT EXISTS idx_box_funnel_events_vid ON box_funnel_events (vid, event, at DESC);
CREATE INDEX IF NOT EXISTS idx_box_funnel_events_claim ON box_funnel_events (claim) WHERE claim IS NOT NULL;

-- ---------------------------------------------------------------------------
-- box_grants — the concierge writing back which claim received which box.
-- ---------------------------------------------------------------------------
-- MANDATORY, not optional. Provisioning is manual in the private preview, so this
-- is one admin endpoint (POST /api/v1/admin/box/grants) or one psql insert. But
-- without it the brief's headline metric — "did they come back and use the box a
-- second time" — has no data source at all: the door never learns what happened
-- to a box handed out by hand, and no amount of log parsing recovers it.
--
-- box_id is a bare UUID rather than a foreign key because the boxes table does
-- not exist yet (ФАЗА 2). Adding the FK is a one-line follow-up once it does.
--
-- The primary key is (claim, box_id), NOT claim alone. A box has an 8h TTL and is
-- reaped after 72h of sleep (ФАЗА 7), so the second use a day later is very often
-- a SECOND box handed to the same claim. With claim as the sole key the concierge
-- would have nowhere to record that grant, and the one metric this table exists
-- for would be unmeasurable in precisely the case that proves the hypothesis.
CREATE TABLE IF NOT EXISTS box_grants (
    claim      TEXT        NOT NULL,
    org_id     TEXT        NOT NULL,
    box_id     UUID        NOT NULL,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    granted_by TEXT        NOT NULL,
    PRIMARY KEY (claim, box_id)
);

CREATE INDEX IF NOT EXISTS idx_box_grants_granted_at ON box_grants (granted_at DESC);
CREATE INDEX IF NOT EXISTS idx_box_grants_box_id ON box_grants (box_id);

-- ---------------------------------------------------------------------------
-- box_repeat_use_7d — the pinned repeat-use definition, as SQL.
-- ---------------------------------------------------------------------------
-- The definition, verbatim, also written into docs/runbooks/box-funnel-metrics.md:
--
--   A SESSION is a contiguous run of active minutes for one box; a gap of more
--   than 30 minutes starts a new session. A claim counts as REPEAT USE if it has
--   >= 2 sessions whose starts are >= 24h apart, both within 7 days of first
--   activation.
--
-- GATED, ON PURPOSE. The view reads a box active-minute ledger that does not
-- exist yet: the box object lands in ФАЗА 2 and the minute ledger in ФАЗА 7. The
-- expected shape is fixed here so the ledger is built against it:
--
--   box_usage(box_id UUID, minute_start TIMESTAMPTZ, kind TEXT, ...)
--   one row per ACTIVE minute per kind; an idle minute produces no row at all.
--
-- So the block below creates the view only if that relation is already present,
-- and otherwise raises a NOTICE and moves on. It does NOT invent a substitute:
-- a view that returned zero rows over a table that does not exist would read as
-- "nobody came back", which is a proxy dressed as a measurement — the mistake
-- this repository has already paid for twice (tasks/lessons.md).
--
-- Follow-up marker: tasks/box-backlog.md, ФАЗА 7 — the migration that creates
-- box_usage must re-run this exact block. The canonical SQL is duplicated in the
-- runbook so it cannot be lost with this file.
DO $$
BEGIN
  IF to_regclass('public.box_usage') IS NULL THEN
    RAISE NOTICE 'box_repeat_use_7d NOT created: box_usage (the active-minute ledger) does not exist yet. Re-run this block from 060_box_leads.sql when it lands (ФАЗА 7).';
    RETURN;
  END IF;

  EXECUTE $view$
    CREATE OR REPLACE VIEW box_repeat_use_7d AS
    -- One row per (claim, box, active minute). Collapses the `kind` dimension so
    -- a minute billed under two kinds is still one minute of activity.
    WITH active_minutes AS (
        SELECT g.claim, u.box_id, u.minute_start
          FROM box_grants g
          JOIN box_usage  u ON u.box_id = g.box_id
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

-- Explicit grants. 033_regrant_dada_all_tables.sql records why the default
-- privileges cannot be relied on: a table created by a role whose default
-- privileges do not include dada silently lacks access, which is how the
-- build-agent's deploy handoff broke on domain_hostnames. Name the tables here
-- so this migration is self-sufficient regardless of which role applies it.
GRANT SELECT, INSERT, UPDATE, DELETE ON box_leads, box_funnel_events, box_grants TO dada;
GRANT USAGE, SELECT ON SEQUENCE box_funnel_events_id_seq TO dada;
