CREATE TABLE IF NOT EXISTS agent_chat_messages (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_sub   TEXT        NOT NULL,
    org_id     TEXT,
    project_id UUID,
    env_id     UUID,
    role       TEXT        NOT NULL,
    content    TEXT        NOT NULL DEFAULT '',
    tool_name  TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_agent_chat_messages_user_created ON agent_chat_messages (user_sub, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_chat_messages_project ON agent_chat_messages (project_id, created_at DESC);
