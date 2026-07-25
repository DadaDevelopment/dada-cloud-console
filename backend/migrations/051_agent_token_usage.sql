CREATE TABLE IF NOT EXISTS agent_token_usage (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    source              TEXT        NOT NULL DEFAULT 'console_chat',
    org_id              TEXT,
    project_id          UUID,
    env_id              UUID,
    user_sub            TEXT,
    model               TEXT        NOT NULL,
    prompt_tokens       BIGINT      NOT NULL DEFAULT 0,
    completion_tokens   BIGINT      NOT NULL DEFAULT 0,
    total_tokens        BIGINT      NOT NULL DEFAULT 0,
    cost_usd            NUMERIC(14,6) NOT NULL DEFAULT 0,
    platform_request_id TEXT,
    cloud_task_id       TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_agent_token_usage_org_created
    ON agent_token_usage (org_id, created_at);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_token_usage_platform_request
    ON agent_token_usage (platform_request_id) WHERE platform_request_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_token_usage_cloud_task
    ON agent_token_usage (cloud_task_id) WHERE cloud_task_id IS NOT NULL;
