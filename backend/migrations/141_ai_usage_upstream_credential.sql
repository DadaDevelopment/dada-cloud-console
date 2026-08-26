ALTER TABLE agent_token_usage
    ADD COLUMN IF NOT EXISTS upstream_credential_id UUID
        REFERENCES ai_gateway_key_credentials(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_agent_token_usage_upstream_credential_created
    ON agent_token_usage (upstream_credential_id, created_at)
    WHERE upstream_credential_id IS NOT NULL;
