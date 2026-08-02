-- 080_ai_routing_mode_and_billing.sql
-- Make the AI Gateway ledger able to answer "whose key paid for this call, and
-- what do we charge for it".
--
-- agent_token_usage already records what a call cost us (cost_usd), but not
-- whether it ran on the customer's own provider key or on the platform's, and
-- not what the customer owes. Without those two facts routed traffic cannot be
-- attributed or billed, which is the whole of the managed-key product.
--
-- key_owner defaults to 'unknown' so historical rows stay honest: they predate
-- the distinction and must not be silently counted as platform-routed revenue.
--
-- billed_usd is the customer-facing amount (our cost plus routing markup), kept
-- separate from cost_usd so margin stays readable and a markup change never
-- rewrites what a call actually cost.

ALTER TABLE agent_token_usage
    ADD COLUMN IF NOT EXISTS key_owner  TEXT          NOT NULL DEFAULT 'unknown',
    ADD COLUMN IF NOT EXISTS billed_usd NUMERIC(14,6) NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_agent_token_usage_project_created
    ON agent_token_usage (project_id, created_at);

-- ai_routing_settings is the per-project answer to the two cards on the
-- LLM-providers page: 'byok' means the project brings its own provider keys,
-- 'platform' means it opted into routing on our key and agrees to be billed
-- for it.
--
-- Absence of a row means 'byok', which is exactly today's behaviour: a project
-- with no credential of its own still reaches the free tier aliases through the
-- platform fallback credential and is billed nothing. Opting in is what starts
-- the meter, so no existing project begins paying because this shipped.
CREATE TABLE IF NOT EXISTS ai_routing_settings (
    project_id UUID        PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    mode       TEXT        NOT NULL DEFAULT 'byok' CHECK (mode IN ('byok', 'platform')),
    updated_by TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
