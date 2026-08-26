-- The internal and platform projects are platform-owned credential sources.
-- Copy their encrypted values into the real global pool without decrypting or
-- deleting the legacy rows. fin-core and every other tenant remain isolated.
INSERT INTO ai_gateway_key_credentials
    (gateway_key_id, provider, label, api_base, api_key_encrypted,
     enabled, priority, status, legacy_provider_credential_id)
SELECT NULL, legacy.provider, 'Imported ' || project.name || ' ' || legacy.provider,
       legacy.api_base, legacy.api_key_encrypted, TRUE, 100, 'pending_discovery', legacy.id
  FROM ai_provider_credentials legacy
  JOIN projects project ON project.id = legacy.project_id
 WHERE project.name IN ('internal', 'platform')
ON CONFLICT (legacy_provider_credential_id)
    WHERE legacy_provider_credential_id IS NOT NULL
DO NOTHING;

-- Promote the pre-existing project-less platform credential as well. Rows are
-- selected by provenance, never by inspecting their encrypted value.
UPDATE ai_gateway_key_credentials credential
   SET enabled = TRUE,
       status = 'pending_discovery',
       unavailable_until = NULL,
       updated_at = NOW()
  FROM ai_provider_credentials legacy
  LEFT JOIN projects project ON project.id = legacy.project_id
 WHERE credential.legacy_provider_credential_id = legacy.id
   AND (legacy.project_id IS NULL OR project.name IN ('internal', 'platform'));

-- Seed stable gateway aliases so activation is atomic. A subsequent upstream
-- /models discovery adds raw wire ids while the control plane preserves these
-- aliases for the credential's effective provider.
WITH provider_alias(provider, model_id) AS (
    VALUES
      ('openai','gpt-4o'), ('openai','gpt-4o-mini'), ('openai','text-embedding-3-small'),
      ('anthropic','claude'), ('anthropic','claude-haiku'),
      ('sotamodel','sota-opus'), ('sotamodel','sota-opus-xhigh'), ('sotamodel','sota-opus-max'),
      ('openrouter','or-gpt-41-mini'), ('openrouter','or-gpt-41-mini-online'),
      ('openrouter','or-gpt-4o-mini'), ('openrouter','openrouter-llama'),
      ('groq','groq-gpt-oss'), ('groq','groq-llama'), ('groq','search'), ('groq','fast'),
      ('sambanova','sambanova-llama'), ('sambanova','medium'),
      ('nvidia_nim','fast'), ('nvidia_nim','medium'), ('nvidia_nim','smart'), ('nvidia_nim','vision')
)
INSERT INTO ai_gateway_key_credential_models (credential_id, model_id)
SELECT credential.id, alias.model_id
  FROM ai_gateway_key_credentials credential
  JOIN ai_provider_credentials legacy ON legacy.id = credential.legacy_provider_credential_id
  LEFT JOIN projects project ON project.id = legacy.project_id
  JOIN provider_alias alias ON alias.provider = credential.provider
 WHERE credential.gateway_key_id IS NULL
   AND credential.deleted_at IS NULL
   AND (legacy.project_id IS NULL OR project.name IN ('internal', 'platform'))
ON CONFLICT (credential_id, model_id) DO NOTHING;
