-- Multiple independently managed upstream credentials behind one AI Gateway
-- key. Secrets remain encrypted with GITOPS_ENCRYPTION_KEY and are only
-- decrypted by the internal candidate endpoint. This intentionally does not
-- replace project-scoped ai_provider_credentials: ServiceIdentity and rolling
-- gateway deployments continue using that legacy contract.
CREATE TABLE IF NOT EXISTS ai_gateway_key_credentials (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- NULL is the global platform pool used by every customer gateway key.
    -- A concrete id is reserved for platform-managed per-key overrides.
    gateway_key_id    UUID        REFERENCES ai_gateway_keys(id) ON DELETE CASCADE,
    provider          TEXT        NOT NULL,
    label             TEXT        NOT NULL DEFAULT '',
    api_base          TEXT,
    api_key_encrypted BYTEA       NOT NULL,
    enabled           BOOLEAN     NOT NULL DEFAULT TRUE,
    priority          INTEGER     NOT NULL DEFAULT 100 CHECK (priority >= 0),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ,
    status            TEXT        NOT NULL DEFAULT 'healthy',
    unavailable_until TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_ai_gateway_key_credentials_candidates
    ON ai_gateway_key_credentials (gateway_key_id, provider, priority, created_at, id)
    WHERE enabled AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_ai_gateway_key_credentials_global_candidates
    ON ai_gateway_key_credentials (provider, priority, created_at, id)
    WHERE enabled AND gateway_key_id IS NULL AND deleted_at IS NULL;

-- Secret-free discovery snapshot reported by the gateway after querying an
-- upstream with one concrete credential. Removing/revoking the credential
-- removes its models, so the UI union cannot retain ghost availability.
CREATE TABLE IF NOT EXISTS ai_gateway_key_credential_models (
    credential_id UUID        NOT NULL REFERENCES ai_gateway_key_credentials(id) ON DELETE CASCADE,
    model_id      TEXT        NOT NULL,
    discovered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (credential_id, model_id)
);

CREATE INDEX IF NOT EXISTS idx_ai_gateway_key_credential_models_model
    ON ai_gateway_key_credential_models (model_id, credential_id);
