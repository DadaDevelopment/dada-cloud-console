-- The anonymous half of a walk belonged to nobody.
--
-- ux_events.user_id is resolved server-side from the dada_uid cookie, so every
-- event before login is stored with user_id NULL. That is correct at write
-- time and useless at read time: the landing view, the CTA click and the
-- signup_started goal -- the top of the funnel, the part that answers "where
-- did this customer come from" -- sat under an anon_id that no audit row, no
-- chat row and no account ever referenced. On live data 3 browsers have rows
-- on BOTH sides of their login and not one query could cross that line.
--
-- ux_identity is the crossing. anon_id lives in localStorage and survives the
-- Keycloak round trip, so the same browser id is present before and after the
-- account exists; once any event on it is attributed, the whole browser's
-- history is attributable retroactively.
--
-- A browser that signed into TWO accounts resolves to NEITHER: HAVING
-- count(DISTINCT user_id) = 1. A shared machine is real, and inventing an
-- owner for it would put one person's pre-login path on another person's
-- funnel -- exactly the class of quiet lie this file exists to remove.
--
-- array_agg picks the single surviving id because PostgreSQL has no min(uuid);
-- the HAVING above is what guarantees there is only one to pick.
CREATE OR REPLACE VIEW ux_identity AS
SELECT
    x.anon_id                AS anon_id,
    (array_agg(DISTINCT x.user_id))[1] AS user_id
FROM ux_events x
WHERE x.anon_id IS NOT NULL
  AND x.user_id IS NOT NULL
GROUP BY x.anon_id
HAVING count(DISTINCT x.user_id) = 1;

GRANT SELECT ON ux_identity TO dada;

-- user_path (073/074/075) gains resolved_user_id as a trailing column: the id
-- to group a path by, as opposed to user_id, which is the id that was known
-- when the row was written. They differ on exactly the rows that matter.
--
-- account_kind changes meaning slightly with it, and deliberately: 075 left it
-- NULL on every anonymous ux row, which was right while those rows were
-- unattributable. A stitched row now carries the cohort of the account it was
-- stitched to, so filtering a funnel to customers no longer amputates the
-- pre-login half of a customer's own walk. A row that was never stitched keeps
-- NULL -- a visitor with no account is the top of the funnel, not an exclusion.
CREATE OR REPLACE VIEW user_path AS
SELECT
    'ux'::text                        AS source,
    x.user_id                         AS user_id,
    x.anon_id                         AS anon_id,
    x.session_id                      AS session_id,
    x.occurred_at                     AS occurred_at,
    x.event_type::text                AS action,
    NULLIF(x.target, '')::text        AS subject,
    NULLIF(x.path, '')::text          AS path,
    NULL::uuid                        AS project_id,
    NULL::uuid                        AS environment_id,
    NULL::text                        AS outcome,
    x.props                           AS detail,
    ka.account_kind                   AS account_kind,
    COALESCE(x.user_id, i.user_id)    AS resolved_user_id
FROM ux_events x
LEFT JOIN ux_identity i ON i.anon_id = x.anon_id
LEFT JOIN user_accounts ka ON ka.id = COALESCE(x.user_id, i.user_id)
UNION ALL
SELECT
    'audit'::text,
    a.actor_id,
    NULL::uuid,
    NULL::uuid,
    a.created_at,
    a.action::text,
    NULLIF(a.resource_name, '')::text,
    NULLIF(a.resource_kind, '')::text,
    a.project_id,
    a.environment_id,
    a.outcome::text,
    COALESCE(a.metadata, '{}'::jsonb),
    kb.account_kind,
    a.actor_id
FROM audit_events a
LEFT JOIN user_accounts kb ON kb.id = a.actor_id
UNION ALL
SELECT
    'chat'::text,
    u.id,
    NULL::uuid,
    NULL::uuid,
    m.created_at,
    'ChatMessage'::text,
    NULLIF(m.tool_name, '')::text,
    NULL::text,
    m.project_id,
    m.env_id,
    m.role::text,
    '{}'::jsonb,
    kc.account_kind,
    u.id
FROM agent_chat_messages m
LEFT JOIN users u ON u.id::text = m.user_sub
LEFT JOIN user_accounts kc ON kc.id = u.id;

GRANT SELECT ON user_path TO dada;
