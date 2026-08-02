-- Money that arrives once is not revenue.
--
-- Every paid plan so far has been a one-off: a customer pays 990 RUB, gets a
-- 30-day term, and 30 days later the account silently lapses to free unless a
-- human remembers to pay again. That is not a subscription business, it is a
-- donation drive with reminder mail.
--
-- YooKassa's recurring flow needs exactly one thing persisted: the
-- payment_method id returned by the FIRST payment when it was created with
-- save_payment_method=true. Later charges are a plain create-payment call
-- carrying payment_method_id and no confirmation block, so the customer is
-- never redirected again.
--
-- autopay_enabled is the customer's consent, captured at checkout and
-- revocable from the console at any time. Revoking clears autopay_method_id
-- as well: a saved card the customer has told us to forget must stop being a
-- charge we CAN make, not merely one we choose not to.
--
-- autopay_failures / autopay_last_attempt_at bound the retry loop. The
-- attempt timestamp doubles as the cross-replica claim: a replica may only
-- charge an account whose last attempt is older than the retry interval, so
-- three pods on one ticker cannot triple-charge one card.
--
-- payments.is_recurring separates the two kinds of row for support and for
-- reporting: a checkout the customer watched happen versus a charge that ran
-- while they slept.
--
-- The quota_breach_* pair makes grandfathering audible. Grace has always let
-- an over-limit org keep creating resources; it did so without a trace, so an
-- org drifting further past the free quotas looked identical to a compliant
-- one until the grace date arrived and creation began failing out of nowhere.
-- Counting the breaches lets both the console banner and the grace reminder
-- mail say what will actually stop working, and when.

ALTER TABLE billing_accounts ADD COLUMN IF NOT EXISTS autopay_enabled         BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE billing_accounts ADD COLUMN IF NOT EXISTS autopay_method_id       TEXT    NOT NULL DEFAULT '';
ALTER TABLE billing_accounts ADD COLUMN IF NOT EXISTS autopay_method_title    TEXT    NOT NULL DEFAULT '';
ALTER TABLE billing_accounts ADD COLUMN IF NOT EXISTS autopay_failures        INTEGER NOT NULL DEFAULT 0;
ALTER TABLE billing_accounts ADD COLUMN IF NOT EXISTS autopay_last_attempt_at TIMESTAMPTZ;
ALTER TABLE billing_accounts ADD COLUMN IF NOT EXISTS quota_breach_count      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE billing_accounts ADD COLUMN IF NOT EXISTS quota_breach_last_at    TIMESTAMPTZ;

ALTER TABLE payments ADD COLUMN IF NOT EXISTS is_recurring BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_billing_accounts_autopay
    ON billing_accounts (plan_expires_at)
    WHERE autopay_enabled AND autopay_method_id <> '';

GRANT SELECT, INSERT, UPDATE, DELETE ON billing_accounts TO dada;
GRANT SELECT, INSERT, UPDATE, DELETE ON payments TO dada;
