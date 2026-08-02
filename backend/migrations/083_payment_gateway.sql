-- Internal payment gateway, phase 1 (owner decision: "gejtvej tolko dlja
-- nashih servisov" -- this is NOT multi-tenant and NOT for customer apps).
-- The console already holds YooKassa credentials for its own plan checkout
-- (migration 050, `payments`), but that table is plan-shaped: every succeeded
-- row triggers assignPlanTx and a mismatch alarm keyed off the plan set
-- (billing_mismatch.go). Reusing it for an arbitrary internal-service charge
-- would grant the paying org a plan it never asked for and then page someone
-- about the mismatch. Money for our own services needs its own tables.
--
-- The first caller is a Telegram VPN bot on a bare VPS, outside the cluster,
-- with no inbound HTTP endpoint of its own -- so this is a poll model, not a
-- push model: the caller creates a charge, then polls GET .../charges/:id
-- until it is no longer pending. The YooKassa webhook (single shop-wide
-- receiver, see billing_payments.go) is extended to also flip these rows when
-- the payload's yk id is not one of ours, but the bot cannot rely on that --
-- it has nowhere to receive a callback -- so polling is the delivery
-- mechanism by design, not a stopgap.
--
-- pay_service_keys: one revocable bearer credential per internal service,
-- same shape as app_deploy_hooks (039) and ai_gateway_keys (058) -- only the
-- sha256 hash is stored, the plaintext is shown once at mint time.
--
-- service_charges: one row per charge. UNIQUE (service_key_id, external_ref)
-- is the whole idempotency contract: the caller supplies its own ref (e.g.
-- `tg:12345:plan-30d`), and a retried create with the same ref returns the
-- existing row instead of a second YooKassa payment.
CREATE TABLE IF NOT EXISTS pay_service_keys (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    service      TEXT        NOT NULL UNIQUE,
    token_hash   TEXT        NOT NULL UNIQUE,
    token_prefix TEXT        NOT NULL,
    created_by   UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS service_charges (
    id               UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    service_key_id   UUID           NOT NULL REFERENCES pay_service_keys(id) ON DELETE RESTRICT,
    service          TEXT           NOT NULL,
    external_ref     TEXT           NOT NULL,
    amount_value     NUMERIC(10,2)  NOT NULL,
    currency         TEXT           NOT NULL DEFAULT 'RUB',
    description      TEXT           NOT NULL,
    status           TEXT           NOT NULL DEFAULT 'pending',
    yk_payment_id    TEXT           UNIQUE,
    confirmation_url TEXT,
    metadata         JSONB          NOT NULL DEFAULT '{}'::jsonb,
    created_at       TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ    NOT NULL DEFAULT now(),
    paid_at          TIMESTAMPTZ,
    UNIQUE (service_key_id, external_ref)
);

CREATE INDEX IF NOT EXISTS idx_service_charges_service_created
    ON service_charges (service, created_at DESC);
