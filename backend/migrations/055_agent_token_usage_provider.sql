-- 055_agent_token_usage_provider.sql
-- Add provider attribution to the agent_token_usage ledger.
--
-- Phase 2 (ADR-015): the gateway's LiteLLM callback becomes a ledger writer
-- for its own traffic (BYOK-direct and console-chat alike), and needs to
-- record which upstream provider (anthropic/openai/groq/...) served the
-- call. Nullable + forward-only; existing rows (console_chat, cloud_task)
-- have no provider recorded historically.

ALTER TABLE agent_token_usage ADD COLUMN IF NOT EXISTS provider TEXT;

CREATE INDEX IF NOT EXISTS idx_agent_token_usage_provider_created
    ON agent_token_usage (provider, created_at);
