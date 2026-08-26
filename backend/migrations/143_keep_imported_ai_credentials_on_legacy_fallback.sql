-- Imported credentials remain visible and editable in the unified inventory,
-- while existing traffic continues through the legacy resolver. Enabling one
-- from the admin UI is the explicit cutover to the model-aware pool.
UPDATE ai_gateway_key_credentials credential
   SET enabled = FALSE,
       status = 'legacy',
       updated_at = NOW()
 WHERE credential.legacy_provider_credential_id IS NOT NULL
   AND NOT EXISTS (
       SELECT 1
         FROM ai_gateway_key_credential_models model
        WHERE model.credential_id = credential.id
   );
