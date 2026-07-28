-- Append-only markers for "clear context" clicks. Past agent_chat_messages
-- rows are never deleted (the daily message cap and the confirm/decline audit
-- trail are computed from those rows and must stay accurate); a reset just
-- tells future reads to ignore anything before its cleared_at, for a given
-- user/project/env scope. The most recent row per scope wins.
CREATE TABLE IF NOT EXISTS agent_chat_context_resets (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_sub   TEXT        NOT NULL,
    project_id UUID,
    env_id     UUID,
    cleared_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_agent_chat_context_resets_scope
    ON agent_chat_context_resets (user_sub, project_id, env_id, cleared_at DESC);
