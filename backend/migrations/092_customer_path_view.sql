-- Our own test actor kept walking through the customer funnel.
--
-- 075/078 already sort every actor into customer/internal/synthetic/platform,
-- and 077 hangs that kind on every row of user_path. What was missing was a
-- place where the exclusion is APPLIED. Every path read is written by hand
-- against audit_events, so the filter is retyped -- or forgotten -- once per
-- analysis: nine cycles in a row the synthetic actor
-- eb82167d-a92e-4333-9bfd-3687541ac17b@keycloak.local (our probe, running
-- RetryOperation/TriggerAutofix/app.diagnose against throwaway resources like
-- ghost-audit-probe) had to be struck out by eye.
--
-- The cost is not theoretical. The only app.diagnose row that has ever existed
-- is that actor's. Read without the filter it says "customers use this
-- feature", which is the exact opposite of the truth, and it is the kind of
-- claim a roadmap gets built on.
--
-- customer_path is user_path with the exclusion baked in:
--   * account_kind = 'customer'  -- a real person's action
--   * account_kind IS NULL AND source = 'ux'  -- an un-stitched browser, the
--     top of the funnel. Dropping these would amputate every pre-signup walk,
--     which is the half of the funnel that answers where customers come from.
--     A visitor with no account is not an excluded actor, it is a prospect.
--
-- Internal (@dada-tuda.ru), synthetic (probes, service accounts, seeds) and
-- platform (the system actor) rows stay in user_path, which remains the
-- unfiltered source for "what happened to this app, by anyone". Nothing is
-- deleted here: this is a read shape, not a retention decision.
CREATE OR REPLACE VIEW customer_path AS
SELECT *
FROM user_path
WHERE account_kind = 'customer'
   OR (account_kind IS NULL AND source = 'ux');

GRANT SELECT ON customer_path TO dada;
