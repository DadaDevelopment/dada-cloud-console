-- The size the table had before the reclaim phase rewrote it.
--
-- The freed bytes used to be measured inside one call of the reclaim step:
-- read the size, run the Job, read the size again. The step is re-entered once
-- per tick, though, and it is the LAST re-entry - the one that finds the Job
-- already finished - whose "before" is read after the rewrite. Every completed
-- run therefore reported bytes_freed = 0, including the first real one, which
-- actually returned 226 MB (295 MB -> 69 MB) to a tenant who was told nothing
-- was freed.
--
-- Stamping the size before the Job is created makes the measurement survive
-- both the re-entry and a console pod that rolls mid-rewrite.
ALTER TABLE db_archive_runs
    ADD COLUMN IF NOT EXISTS bytes_before BIGINT NOT NULL DEFAULT 0;
