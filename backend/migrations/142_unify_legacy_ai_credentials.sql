-- Preserve the provenance of credentials imported from the pre-pool store.
-- Only project-less rows are platform-wide. Project-scoped BYOK rows must
-- remain isolated and are exposed to admins as read-only inventory instead.
ALTER TABLE ai_gateway_key_credentials
    ADD COLUMN IF NOT EXISTS legacy_provider_credential_id UUID
        REFERENCES ai_provider_credentials(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_ai_gateway_key_credentials_legacy_source
    ON ai_gateway_key_credentials (legacy_provider_credential_id)
    WHERE legacy_provider_credential_id IS NOT NULL;

INSERT INTO ai_gateway_key_credentials
    (gateway_key_id, provider, label, api_base, api_key_encrypted,
     enabled, priority, legacy_provider_credential_id)
SELECT NULL, legacy.provider, 'Imported platform ' || legacy.provider,
       legacy.api_base, legacy.api_key_encrypted, TRUE, 100, legacy.id
  FROM ai_provider_credentials legacy
 WHERE legacy.project_id IS NULL
ON CONFLICT (legacy_provider_credential_id)
    WHERE legacy_provider_credential_id IS NOT NULL
DO NOTHING;
