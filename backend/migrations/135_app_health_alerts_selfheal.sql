-- The platform-selfheal sweeper (platform_selfheal.go) may get exactly one
-- rebuild attempt per app per fixed signature -- without a persisted mark, a
-- backend restart mid-sweep or a second tick before the queued build lands
-- would see the same crash-looping row again and queue a second rebuild for
-- no reason. Nullable with no default on purpose, same reasoning as
-- builds.retry_after (migration 133): a row written by a replica still
-- running the previous image leaves both columns NULL, which the sweeper's
-- WHERE selfheal_rebuilt_at IS NULL reads as "not yet attempted" -- the same
-- behavior the row already had before this migration existed. NOT NULL here
-- would require every existing app_health_alerts row to be backfilled before
-- old and new replicas could safely read the same row during a rolling
-- deploy, which this table sees on every silent-crash watcher tick.
ALTER TABLE app_health_alerts ADD COLUMN IF NOT EXISTS selfheal_rebuilt_at TIMESTAMPTZ;
ALTER TABLE app_health_alerts ADD COLUMN IF NOT EXISTS selfheal_rebuilt_cause_kind VARCHAR(64);
