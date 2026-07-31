-- The chat half of user_path (migration 073) joined nothing: 103 chat rows, 0
-- of them attached to a user.
--
-- agent_chat_messages.user_sub is NOT a Keycloak sub despite the name. The
-- handler writes claims.UserID.String() (backend/internal/api/agent_chat.go),
-- which is the internal users.id -- the same value audit_events stores in
-- actor_id. Verified live: joining on users.keycloak_sub matches 0 rows,
-- joining on users.id matches all 103, and 2971 audit rows share the value.
--
-- The join is kept against users rather than casting user_sub straight into
-- user_id, so a row left behind by a deleted account resolves to NULL instead
-- of pointing at an id that no longer exists.
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
LEFT JOIN users u ON u.id::text = m.user_sub;
