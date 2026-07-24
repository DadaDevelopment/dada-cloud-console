CREATE TABLE IF NOT EXISTS agent_chat_pending_actions (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_sub          TEXT        NOT NULL,
    org_id            TEXT,
    project_id        UUID,
    env_id            UUID,
    tool_name         TEXT        NOT NULL,
    args_json         JSONB       NOT NULL DEFAULT '{}'::jsonb,
    tool_call_id      TEXT        NOT NULL,
    messages_snapshot JSONB       NOT NULL,
    tool_call_count   INT         NOT NULL DEFAULT 0,
    write_call_count  INT         NOT NULL DEFAULT 0,
    status            TEXT        NOT NULL DEFAULT 'pending',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at        TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_chat_pending_actions_user_status ON agent_chat_pending_actions (user_sub, status);
