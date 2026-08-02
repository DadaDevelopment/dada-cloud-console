-- Records the actual outcome of an app-health alert send attempt, separate
-- from last_sent_at (which only ever meant "cooldown slot claimed", not
-- "email delivered"). Before this, a failed SMTP send was indistinguishable
-- from a successful one in the database: the claim happens before the send
-- (P1-ALERT-OWNERLESS-DROP retry-storm guard), so a bounced or erroring send
-- left last_sent_at stamped and the failure visible only in a log line that
-- may already be gone by the time anyone asks "did this app owner get
-- notified".
--
-- last_send_attempt_at/last_send_ok/last_send_error/last_recipient answer
-- that question with a query instead of a log grep. send_failures counts
-- consecutive failed attempts for the current alert (reset implicitly by the
-- next successful send).
ALTER TABLE app_health_alerts
    ADD COLUMN IF NOT EXISTS last_send_attempt_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_send_ok        BOOLEAN,
    ADD COLUMN IF NOT EXISTS last_send_error     TEXT,
    ADD COLUMN IF NOT EXISTS last_recipient      TEXT,
    ADD COLUMN IF NOT EXISTS send_failures       INTEGER NOT NULL DEFAULT 0;
