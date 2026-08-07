-- Outbound growth campaigns, and the only place their result is recorded.
--
-- The funnel this table exists to answer is "we mailed a dormant account, did
-- anything happen": one row per (campaign, user), four timestamps on the way
-- down. Sent, clicked, redeemed and converted are deliberately separate
-- columns rather than a status enum, because the interesting reads are the
-- gaps between them -- a click that never redeems and a redeem that never
-- converts are different product problems with different fixes.
--
-- token is the campaign's promo link. It is opaque and per-recipient, so the
-- click can be attributed to a person without putting an email address in a
-- URL, and it is what the authenticated redeem endpoint matches against the
-- caller's own user id before granting anything.
--
-- variant carries the A/B arm. It ships unused (every send gets 'a'): the
-- first cohort is ten addresses, where a split measures noise. The column is
-- here so the split becomes a value change rather than a migration once the
-- population is large enough to divide.
CREATE TABLE IF NOT EXISTS growth_campaign_sends (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign     TEXT        NOT NULL,
    variant      TEXT        NOT NULL DEFAULT 'a',
    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email        TEXT        NOT NULL,
    token        TEXT        NOT NULL,
    sent_at      TIMESTAMPTZ,
    clicked_at   TIMESTAMPTZ,
    redeemed_at  TIMESTAMPTZ,
    converted_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One send per person per campaign. This is the dedup guard the sweeper leans
-- on: it re-runs every tick and must never mail the same account twice.
CREATE UNIQUE INDEX IF NOT EXISTS uq_growth_campaign_sends_campaign_user
    ON growth_campaign_sends (campaign, user_id);

CREATE UNIQUE INDEX IF NOT EXISTS uq_growth_campaign_sends_token
    ON growth_campaign_sends (token);

-- The conversion backfill walks rows that were mailed and have not converted
-- yet, so that pair is what the index covers.
CREATE INDEX IF NOT EXISTS idx_growth_campaign_sends_pending_conversion
    ON growth_campaign_sends (sent_at)
    WHERE sent_at IS NOT NULL AND converted_at IS NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON growth_campaign_sends TO dada;
