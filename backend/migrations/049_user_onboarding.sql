CREATE TABLE IF NOT EXISTS user_onboarding (
    user_sub        TEXT        NOT NULL,
    onboarding_key  TEXT        NOT NULL,
    status          TEXT        NOT NULL CHECK (status IN ('seen', 'skipped', 'done')),
    step_reached    INT         NOT NULL DEFAULT 0,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_sub, onboarding_key)
);
