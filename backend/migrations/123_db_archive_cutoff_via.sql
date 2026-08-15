-- Where the cutoff timestamp lives when it is not on the archived table.
--
-- The archive only ever knew how to cut on a column of the table itself, which
-- silently excluded the tables that most need it: on the first real customer
-- database that filled a shard, the largest table (10 GB of 30) has no time
-- column at all - only foreign keys - so no cutoff could ever select a row of
-- it. Recording the parent table, the child's foreign key and the referenced
-- column lets the same run archive by the parent's timestamp.
--
-- Empty strings mean the ordinary case: the cutoff column is on the table being
-- archived, and cutoff_column keeps meaning exactly what it meant before. When
-- these are set, cutoff_column names the column on cutoff_via_table.
ALTER TABLE db_archive_runs
    ADD COLUMN IF NOT EXISTS cutoff_via_table TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cutoff_via_fk    TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cutoff_via_pk    TEXT NOT NULL DEFAULT '';
