-- 094_agent_token_usage_identity.sql
--
-- ADR-021 phase 4: spend follows the same grain as the credential.
--
-- 080 gave the ledger project_id, which answers "which project spent this"
-- and stops there. A project is a set of apps, and the whole reason 087 keys
-- an identity to the app rather than the project is that the app is the unit
-- somebody actually owns: two apps in one project share a project credential,
-- so today their spend is one indistinguishable number. The token already
-- names the app at introspection time -- the gateway just was not carrying
-- that name as far as the ledger.
--
-- Nullable, and it stays nullable: a console-chat call and a project-scoped
-- sk-dada-ai- key have no identity behind them, and backfilling a made-up one
-- would turn "not attributable" into a wrong attribution. Rows written before
-- this migration keep NULL for the same reason -- the identity that paid is
-- not recoverable from a row that never recorded it.
--
-- ON DELETE SET NULL, unlike 089's RESTRICT on service_charges: a charge is an
-- accounting record whose owner must not vanish, while this ledger is
-- observability. Deleting an app should not be blocked by a year of usage
-- rows, and the project_id/cost stay correct without the identity.
ALTER TABLE agent_token_usage
    ADD COLUMN IF NOT EXISTS identity_id UUID REFERENCES service_identities(id) ON DELETE SET NULL;

-- Partial: the per-app breakdown only ever reads rows that have an identity,
-- and the NULL rows are the majority (every pre-094 row, plus console chat).
CREATE INDEX IF NOT EXISTS idx_agent_token_usage_identity_created
    ON agent_token_usage (identity_id, created_at DESC)
    WHERE identity_id IS NOT NULL;
