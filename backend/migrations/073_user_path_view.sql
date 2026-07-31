-- One timeline per user, across the three tables that each held a third of it.
--
-- Until now a funnel question needed three unrelated queries and a manual
-- stitch: ux_events (what the browser did), audit_events (what the backend was
-- asked to do), agent_chat_messages (what the user said when they got stuck).
-- They do not even agree on a key: audit_events.actor_id is users.id, while
-- agent_chat_messages.user_sub is the Keycloak sub -- so "what did this person
-- try right before they asked for help" was not expressible at all.
--
-- Yandex.Metrika goals are the fourth source and stay outside the database by
-- construction (anonymous, sampled, blocked for part of the audience, no join
-- to users). They arrive here instead as ux_events rows of type 'goal', which
-- the client mirrors at reachGoal time -- see frontend/lib/metrika.ts.
--
-- anon_id is carried through so the pre-login half of a walk (user_id NULL)
-- can be attached to the account the same browser later signed into.
--
-- Chat CONTENT is deliberately not exposed: it is free text a user typed and
-- may carry anything, and this view exists for path analysis, not for reading
-- people's messages. Only the timing, the project and the tool name cross over.
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
    x.props                           AS detail
FROM ux_events x
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
    COALESCE(a.metadata, '{}'::jsonb)
FROM audit_events a
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
    '{}'::jsonb
FROM agent_chat_messages m
LEFT JOIN users u ON u.keycloak_sub = m.user_sub;

-- Same reasoning as 062/066/069: an object created by the migration role is not
-- covered by an earlier GRANT ON ALL TABLES, and the symptom would surface far
-- from here.
GRANT SELECT ON user_path TO dada;
