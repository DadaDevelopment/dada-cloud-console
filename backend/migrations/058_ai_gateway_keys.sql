-- 058_ai_gateway_keys.sql
-- Project-scoped AI Gateway keys: the self-service credential a user mints in
-- the console and puts straight into an OpenAI-compatible SDK
-- (base_url = the gateway, api_key = this token). Same shape as
-- app_deploy_hooks (039): only the sha256 hash is stored, the plaintext is
-- shown once at creation time.
--
-- These keys are introspected by the gateway plugin against
-- POST /internal/ai/key/introspect. They are deliberately a separate namespace
-- from user-service's sk-dada keys (which need a Keycloak realm-admin role to
-- mint, so they can never be self-service): the `sk-dada-ai-` prefix routes the
-- gateway to the right introspection backend without a second round trip.

CREATE TABLE IF NOT EXISTS ai_gateway_keys (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id   UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name         TEXT        NOT NULL DEFAULT '',
    scopes       TEXT        NOT NULL DEFAULT 'ai:chat ai:embeddings',
    token_hash   TEXT        NOT NULL UNIQUE,
    token_prefix TEXT        NOT NULL,
    created_by   UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_ai_gateway_keys_active
    ON ai_gateway_keys (project_id)
    WHERE revoked_at IS NULL;
