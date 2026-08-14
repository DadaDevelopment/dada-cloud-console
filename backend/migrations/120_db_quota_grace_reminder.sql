-- One reminder before a grace window closes.
--
-- The grace letter arrives when the limit first lands on data that already
-- existed, which is a day before anything happens; the reminder is the one that
-- reaches an owner who read the first letter and put it aside. It must be sent
-- exactly once per grace window, so the send is claimed by a conditional UPDATE
-- on this column rather than decided from the deadline alone -- the watcher
-- ticks every 30 minutes, and "less than 6 hours left" is true for twelve
-- consecutive ticks.
--
-- The column is cleared whenever a new grace window opens, so a database that
-- goes over quota again later gets its reminder again.
ALTER TABLE db_quota_state
    ADD COLUMN IF NOT EXISTS grace_reminded_at TIMESTAMPTZ;
