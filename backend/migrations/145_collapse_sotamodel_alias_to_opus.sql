-- Customers select a stable capability, not a supplier or reasoning preset.
-- Keep the upstream credential/provider private and collapse the three
-- SotaModel-specific public names into one gateway-owned alias.
DELETE FROM ai_gateway_key_credential_models
 WHERE model_id IN ('sota-opus', 'sota-opus-xhigh', 'sota-opus-max');

INSERT INTO ai_gateway_key_credential_models (credential_id, model_id)
SELECT id, 'opus'
  FROM ai_gateway_key_credentials
 WHERE provider = 'sotamodel' AND deleted_at IS NULL
ON CONFLICT (credential_id, model_id) DO NOTHING;
