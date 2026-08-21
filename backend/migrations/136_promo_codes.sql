-- Promo code redemption: a paid-plan grant independent of the payment path.
--
-- Built for the Telegram student-startup-grant campaign (2802 members,
-- 2026-08-21): the owner publicly promised "промокод для участников чата"
-- and no promo/coupon feature existed anywhere in the codebase. A promo is
-- deliberately kept out of `payments` -- it must never look like a payment
-- in the money ledger or in the "first succeeded payment from a non-owner"
-- gate metric. It only ever touches billing_accounts.plan /
-- plan_expires_at, the same two columns SweepPlanExpiry and the checkout
-- path already own.
--
-- promo_codes.code is stored upper-cased and compared upper-cased (see
-- backend/internal/api/billing_promo.go) rather than adding a citext
-- dependency this database has never needed before.
CREATE TABLE IF NOT EXISTS promo_codes (
    code             TEXT        PRIMARY KEY,
    plan             TEXT        NOT NULL,
    days             INTEGER     NOT NULL CHECK (days > 0),
    max_redemptions  INTEGER     NOT NULL CHECK (max_redemptions > 0),
    redeemed_count   INTEGER     NOT NULL DEFAULT 0 CHECK (redeemed_count >= 0),
    valid_until      TIMESTAMPTZ,
    note             TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One redemption per (code, billing account): the unique index is what
-- makes "already redeemed by this org" a constraint the database enforces
-- rather than a race the handler has to detect on its own.
CREATE TABLE IF NOT EXISTS promo_redemptions (
    id           UUID        NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    code         TEXT        NOT NULL REFERENCES promo_codes(code),
    org_id       TEXT        NOT NULL,
    user_id      UUID        NOT NULL,
    redeemed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (code, org_id)
);

CREATE INDEX IF NOT EXISTS idx_promo_redemptions_org ON promo_redemptions (org_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON promo_codes TO dada;
GRANT SELECT, INSERT, UPDATE, DELETE ON promo_redemptions TO dada;

-- The one real code for the 2026-08 student-startup Telegram campaign.
-- max_redemptions is generous on purpose: the chat has 2802 members and the
-- owner made the offer to all of them, not to a capped first batch.
INSERT INTO promo_codes (code, plan, days, max_redemptions, valid_until, note)
VALUES (
    'STUDSTARTUP',
    'startup',
    30,
    2000,
    '2026-12-31 23:59:59+00',
    'telegram student-startup grant chat campaign, 2026-08'
)
ON CONFLICT (code) DO NOTHING;
