-- app_health_alerts cooldown escalation.
--
-- Owner feedback (2026-08-30, gateway CrashLoopBackOff in internal-prod):
-- the flat 24h cooldown means an app that stays broken gets exactly one
-- email every day forever -- a stuck crashloop two-plus days old already
-- read as spam, and nothing about it gets more actionable with repetition.
--
-- first_detected_at is when THIS continuous incident started: the earliest
-- of the current bad-state streak. It is set on INSERT and advanced only
-- when the detected reason CHANGES (ON CONFLICT path: a CrashLoopBackOff
-- that flips to OOMKilled is a new incident; the same reason on every tick
-- is the same incident). A row for a recovered app keeps its old value
-- harmlessly: a new incident after recovery always arrives through a
-- reason change or a fresh row. Legacy rows (written before this column)
-- get last_sent_at as the backfilled value, which only ever makes the
-- incident look older than it is -- i.e. one escalation step closer to
-- silence, never an extra email.
--
-- first_detected_at is what the escalated cooldown is measured against in
-- claimAppHealthAlertSlot: 24h while the incident is young, 72h after
-- three days of the same failure, seven days after two weeks. The reset on
-- reason change lives in the same upserts that maintain this column.
ALTER TABLE app_health_alerts
    ADD COLUMN IF NOT EXISTS first_detected_at TIMESTAMPTZ;

UPDATE app_health_alerts
    SET first_detected_at = last_sent_at
    WHERE first_detected_at IS NULL;

ALTER TABLE app_health_alerts
    ALTER COLUMN first_detected_at SET DEFAULT to_timestamp(0);

ALTER TABLE app_health_alerts
    ALTER COLUMN first_detected_at SET NOT NULL;
