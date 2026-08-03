-- ADR-021 phase 4: a monthly AI spend ceiling that belongs to the app, not the
-- project. Attribution (094) made per-app cost answerable; this is what lets an
-- answer become a limit. NULL means no ceiling, which is what every existing
-- identity gets: a migration must not start rejecting live traffic.
--
-- NUMERIC(14,6) matches agent_token_usage.cost_usd, so the comparison never
-- crosses a type boundary and a fraction of a cent stays a fraction of a cent.

ALTER TABLE service_identities
    ADD COLUMN IF NOT EXISTS ai_monthly_limit_usd NUMERIC(14,6);

ALTER TABLE service_identities
    DROP CONSTRAINT IF EXISTS service_identities_ai_budget_nonneg;

ALTER TABLE service_identities
    ADD CONSTRAINT service_identities_ai_budget_nonneg
    CHECK (ai_monthly_limit_usd IS NULL OR ai_monthly_limit_usd >= 0);
