-- Client-side UX telemetry: the console had no record of anything a user did
-- that did not reach a mutating endpoint.
--
-- audit_events is, by construction, a journal of authorized backend WRITE
-- actions. Migration 068 bolted three passive signals onto it (SessionStart,
-- ViewBuildLogs, ViewAppLogs) and that is the ceiling of that approach: opening
-- a page, poking Settings tabs, opening and closing a modal, or pressing a
-- button that did nothing exists in no table at all. Yandex.Metrika cannot fill
-- the gap either -- it is anonymous, sampled, and does not join to users.
--
-- Deliberately a SEPARATE table, not more columns on audit_events: that journal
-- is read as a legal record of who changed what, and product telemetry at
-- click volume would both bury it and change what it means.
--
-- anon_id is the browser-scoped id the frontend mints and keeps in
-- localStorage; it survives login, which is what stitches the pre-signup visit
-- to the account. user_id is resolved server-side from the dada_uid cookie
-- (Keycloak sub -> users.keycloak_sub), the same id published to Metrika, so
-- visit -> goal -> click -> audit action -> build -> live URL is one chain.
--
-- PII: target holds control names and paths only. Field VALUES are never sent
-- by the client and must never be stored here (152-ФЗ).
CREATE TABLE IF NOT EXISTS ux_events (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    anon_id     UUID,
    session_id  UUID,
    event_type  VARCHAR(40)  NOT NULL,
    path        VARCHAR(500) NOT NULL DEFAULT '',
    target      VARCHAR(200) NOT NULL DEFAULT '',
    props       JSONB        NOT NULL DEFAULT '{}',
    occurred_at TIMESTAMPTZ  NOT NULL,
    received_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Per-user path reconstruction, the anonymous pre-login half of the same walk,
-- one session replayed click by click, and funnel slices by event type.
CREATE INDEX IF NOT EXISTS idx_ux_events_user_time ON ux_events (user_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_ux_events_anon_time ON ux_events (anon_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_ux_events_session ON ux_events (session_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_ux_events_type_time ON ux_events (event_type, occurred_at DESC);

-- Same reasoning as 062/066: a table created by a migration role is not covered
-- by earlier GRANT ON ALL TABLES, and the symptom would surface far from here.
GRANT SELECT, INSERT, UPDATE, DELETE ON ux_events TO dada;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO dada;
