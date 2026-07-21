-- 040_feedback.sql
-- In-product feedback capture: lets a logged-in (or anonymous, if the bearer
-- is missing/expired) user send a short message from any console page. The
-- routine reads this table directly over psql, bypassing the broken IMAP
-- inbox the old mailto support link relied on.

CREATE TABLE IF NOT EXISTS feedback (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_sub   TEXT,
    org_id     TEXT,
    route      TEXT        NOT NULL DEFAULT '',
    message    TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_feedback_created_at ON feedback (created_at DESC);
