-- Agent platform conversation state: platform-owned conversation runtime for
-- agents. Stores conversation identity, message history, and lifecycle metadata
-- independent of individual agent implementations. See docs/plans/agent-harness-conversation-runtime.md

-- conversations table: one row per ongoing dialogue between an agent and an
-- external actor (Telegram user, Slack user, etc). The (agent_name, channel,
-- external_id) tuple is the stable identity: when the same telegram chat_id
-- messages the same agent again, we resolve back to the same conversation row
-- and its accumulated metadata (CRM person_id, custom fields, etc).
CREATE TABLE IF NOT EXISTS conversations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_name TEXT NOT NULL,
    channel TEXT NOT NULL,  -- 'telegram', 'slack', 'discord', etc
    external_id TEXT NOT NULL,  -- telegram chat_id, slack channel_id
    actor_external_id TEXT,  -- telegram user_id, slack user_id
    actor_username TEXT,
    actor_metadata JSONB DEFAULT '{}',  -- first_name, avatar_url, etc
    metadata JSONB DEFAULT '{}',  -- crm_person_id, tags, custom app data
    status TEXT NOT NULL DEFAULT 'active',  -- 'active', 'closed', 'archived'
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(agent_name, channel, external_id)
);

CREATE INDEX idx_conversations_agent_status ON conversations(agent_name, status);
CREATE INDEX idx_conversations_updated ON conversations(updated_at) WHERE status = 'active';

-- conversation_messages table: full message history for every conversation.
-- Platform storage allows lifecycle hooks, context injection, and analytics
-- independent of agent memory. role follows OpenAI/Anthropic convention:
-- 'user', 'assistant', 'system'.
CREATE TABLE IF NOT EXISTS conversation_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    role TEXT NOT NULL,  -- 'user', 'assistant', 'system'
    content TEXT NOT NULL,
    metadata JSONB DEFAULT '{}',  -- tool_calls, citations, token_count, etc
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_conversation_messages_conversation ON conversation_messages(conversation_id, created_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON conversations TO dada;
GRANT SELECT, INSERT, UPDATE, DELETE ON conversation_messages TO dada;
