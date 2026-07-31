-- Who counts as a user, in one place.
--
-- The admin overview counted 28 users where 18 real people exist: it filtered
-- Keycloak service accounts and nothing else, so the seed rows (@dada.local),
-- the sub-named shells (@keycloak.local), our own probes (dada-e2e-test@,
-- a5-testuser-%, sp2verify%) and the two staff accounts all landed in the
-- headline number and in every funnel derived from it. A 55% overstatement is
-- not a rounding error -- it is the difference between "half the cohort did
-- nothing" and a real activation rate.
--
-- Deliberately a view over the email/username convention rather than a column:
-- the convention is what actually decides this today, a column would need a
-- write path and would silently drift the first time someone forgot to set it,
-- and a new probe account starts being excluded the moment it is created.
--
-- Three kinds, because "not a customer" has two very different reasons:
--   synthetic -- not a person: seeds, service accounts, e2e and verify probes
--   internal  -- a person, but one of ours; real usage, not demand
--   customer  -- everyone else; the only cohort a funnel may count
--
-- The service-account rule is folded in here so callers stop carrying their own
-- half of the filter (admin_overview.go used to pass 'service-account-%' to
-- three separate queries).
CREATE OR REPLACE VIEW user_accounts AS
SELECT
    u.id,
    u.email,
    u.username,
    u.created_at,
    CASE
        WHEN u.username LIKE 'service-account-%'    THEN 'synthetic'
        WHEN u.email    LIKE '%@keycloak.local'     THEN 'synthetic'
        WHEN u.email    LIKE '%@dada.local'         THEN 'synthetic'
        WHEN u.email    LIKE 'dada-e2e-test@%'      THEN 'synthetic'
        WHEN u.email    LIKE 'a5-testuser-%'        THEN 'synthetic'
        WHEN u.email    LIKE 'sp2verify%'           THEN 'synthetic'
        WHEN u.email    LIKE '%@sp2-verify.%'       THEN 'synthetic'
        WHEN u.email    LIKE '%@dada-tuda.ru'       THEN 'internal'
        ELSE 'customer'
    END AS account_kind
FROM users u;

GRANT SELECT ON user_accounts TO dada;

-- user_path (073/074) gains the same verdict as a trailing column, so a path
-- query filters the cohort where it reads the path instead of joining users
-- again. Anonymous ux rows keep NULL: a visitor with no account yet is not a
-- customer and not excluded either -- they are the top of the funnel.
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
    ka.account_kind                   AS account_kind
FROM ux_events x
LEFT JOIN user_accounts ka ON ka.id = x.user_id
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
    kb.account_kind
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
    kc.account_kind
FROM agent_chat_messages m
LEFT JOIN users u ON u.id::text = m.user_sub
LEFT JOIN user_accounts kc ON kc.id = u.id;
