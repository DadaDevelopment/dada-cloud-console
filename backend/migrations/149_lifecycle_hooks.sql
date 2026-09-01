-- Agent platform lifecycle hooks: declarative trigger → action automation
-- for conversation events. Allows agents to integrate with external systems
-- (CRM, ticketing, analytics) without LLM involvement. See docs/plans/agent-harness-conversation-runtime.md

-- lifecycle_hooks table: one row per hook. Trigger events fire at specific
-- conversation lifecycle points; actions execute HTTP calls, update metadata,
-- or schedule follow-up messages. Template interpolation in action_config
-- uses {{ conversation.metadata.field }}, {{ actor.username }}, etc.
CREATE TABLE IF NOT EXISTS lifecycle_hooks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_name TEXT NOT NULL,
    name TEXT NOT NULL,  -- human-readable hook name for debugging
    trigger_event TEXT NOT NULL,  -- 'conversation.created', 'message.received', 'agent.run.completed', 'conversation.idle'
    trigger_config JSONB DEFAULT '{}',  -- idle_minutes for conversation.idle, filters for message.received
    action_type TEXT NOT NULL,  -- 'http', 'metadata', 'schedule'
    action_config JSONB NOT NULL,  -- URL/method/body for http, key/value for metadata, message for schedule
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(agent_name, name)
);

CREATE INDEX idx_lifecycle_hooks_agent_event ON lifecycle_hooks(agent_name, trigger_event) WHERE enabled = true;

-- hook_executions table: audit log for hook execution history. Allows
-- debugging hook failures and retry logic in future phases.
CREATE TABLE IF NOT EXISTS hook_executions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    hook_id UUID NOT NULL REFERENCES lifecycle_hooks(id) ON DELETE CASCADE,
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    trigger_event TEXT NOT NULL,
    status TEXT NOT NULL,  -- 'success', 'failed', 'retrying'
    error_message TEXT,
    request_data JSONB,
    response_data JSONB,
    executed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_hook_executions_hook ON hook_executions(hook_id, executed_at);
CREATE INDEX idx_hook_executions_conversation ON hook_executions(conversation_id, executed_at);
CREATE INDEX idx_hook_executions_status ON hook_executions(status, executed_at) WHERE status = 'failed';

GRANT SELECT, INSERT, UPDATE, DELETE ON lifecycle_hooks TO dada;
GRANT SELECT, INSERT ON hook_executions TO dada;
