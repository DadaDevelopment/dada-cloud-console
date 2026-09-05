-- Typed runtime context is separate from customer claims and from verified
-- integration outcomes. No sales-specific state machine is imposed here.
CREATE TABLE IF NOT EXISTS conversation_runtime_state (
    conversation_id UUID PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    agent_enabled BOOLEAN NOT NULL DEFAULT true,
    reported_facts JSONB NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(reported_facts) = 'object'),
    open_loops JSONB NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(open_loops) = 'object'),
    active_skills JSONB NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(active_skills) = 'object'),
    pause_reason TEXT NOT NULL DEFAULT '',
    crm_status_sync TEXT NOT NULL DEFAULT '' CHECK (crm_status_sync IN ('', 'pending', 'completed', 'failed')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((agent_enabled AND pause_reason = '' AND crm_status_sync = '') OR
           (NOT agent_enabled AND pause_reason <> '' AND crm_status_sync <> ''))
);
GRANT SELECT, INSERT, UPDATE, DELETE ON conversation_runtime_state TO dada;
