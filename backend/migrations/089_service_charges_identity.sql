-- 089_service_charges_identity.sql
--
-- ADR-021 phase 3: the payment gateway becomes the identity's second audience.
--
-- 083 gave a charge exactly one owner, pay_service_keys.service -- free text
-- chosen by a platform admin at mint time, unrelated to any app, and with no
-- scopes. That is a second credential system for the same principal: an app
-- that both infers and charges needed two keys, and neither knew about the
-- other. Phase 3 lets an sk-dada-id- token pay, gated on the pay:charge scope,
-- so one revocable credential covers every audience (the whole point of 087).
--
-- Both owners stay valid on purpose. dada-vpn-bot holds a live sk-dada-pay-
-- key today; dropping service_key_id would break a paying caller to make a
-- schema tidier. The CHECK makes the pair exclusive instead, so every row has
-- exactly one owner and "who owes this" is never ambiguous.
--
-- ON DELETE RESTRICT mirrors 083's service_key_id: a settled charge is an
-- accounting record, so its owner cannot be deleted out from under it.
ALTER TABLE service_charges
    ADD COLUMN IF NOT EXISTS identity_id UUID REFERENCES service_identities(id) ON DELETE RESTRICT;

ALTER TABLE service_charges
    ALTER COLUMN service_key_id DROP NOT NULL;

ALTER TABLE service_charges
    DROP CONSTRAINT IF EXISTS service_charges_one_owner;

ALTER TABLE service_charges
    ADD CONSTRAINT service_charges_one_owner
    CHECK (num_nonnulls(service_key_id, identity_id) = 1);

-- The idempotency contract of 083 is UNIQUE (service_key_id, external_ref):
-- a retried create with the same ref must return the SAME charge, never a
-- second YooKassa payment. An identity-owned charge has service_key_id NULL,
-- where that UNIQUE is vacuous (NULLs never conflict), so the identity half
-- needs its own partial index or every retry would create a new payment.
CREATE UNIQUE INDEX IF NOT EXISTS uq_service_charges_identity_ref
    ON service_charges (identity_id, external_ref)
    WHERE identity_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_service_charges_identity
    ON service_charges (identity_id, created_at DESC)
    WHERE identity_id IS NOT NULL;
