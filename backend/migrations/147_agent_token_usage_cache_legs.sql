ALTER TABLE agent_token_usage
    ADD COLUMN IF NOT EXISTS cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_creation_tokens BIGINT NOT NULL DEFAULT 0;

COMMENT ON COLUMN agent_token_usage.cache_read_tokens IS
    'Tokens served from the provider prompt cache (billed at the discount rate). prompt_tokens already folds this in; kept separately so effective-vs-list price is computable per row.';

COMMENT ON COLUMN agent_token_usage.cache_creation_tokens IS
    'Tokens written to the provider prompt cache (billed at the write premium). prompt_tokens already folds this in.';

ALTER TABLE agent_token_usage ADD CONSTRAINT agent_token_usage_cache_folds_in_prompt
    CHECK (cache_read_tokens + cache_creation_tokens <= prompt_tokens);
