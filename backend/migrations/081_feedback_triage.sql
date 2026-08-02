-- Support tickets landed in a table nobody read.
--
-- feedback has been write-only since 040: the console POSTs a row, and the
-- only reader is a human running psql. On prod that meant four real requests
-- sat untouched -- two of them the SAME pain ("I uploaded my app from an
-- archive, how do I get my files back out") -- for four to eleven days. A
-- support channel with no notification and no state is a channel that loses
-- customers quietly, which is the one failure mode this platform cannot see.
--
-- Three things are added here.
--
-- status/resolution/resolved_at give a ticket a lifecycle, so "handled" is a
-- fact in the table instead of a memory.
--
-- project_id/app_name give a ticket a TARGET. The console already sends the
-- route it was written from, and a route like
--   /projects/<uuid>/apps/<name>/settings
-- names the app the person was looking at when they gave up. Parsed at write
-- time, it turns a paragraph of prose into something the auto-fix engine can
-- aim at -- the same engine that until now only ever fired from a crash
-- alert.
--
-- cloud_task_id links the ticket to the auto-fix run it produced, so the PR
-- that answers a complaint is reachable from the complaint.
--
-- The backfill re-parses the routes already stored. It is a plain regexp over
-- four rows today; it exists so the first admin view is not empty of exactly
-- the tickets that motivated this file.

ALTER TABLE feedback ADD COLUMN IF NOT EXISTS status        TEXT NOT NULL DEFAULT 'new';
ALTER TABLE feedback ADD COLUMN IF NOT EXISTS project_id    UUID;
ALTER TABLE feedback ADD COLUMN IF NOT EXISTS app_name      TEXT NOT NULL DEFAULT '';
ALTER TABLE feedback ADD COLUMN IF NOT EXISTS cloud_task_id UUID;
ALTER TABLE feedback ADD COLUMN IF NOT EXISTS resolution    TEXT NOT NULL DEFAULT '';
ALTER TABLE feedback ADD COLUMN IF NOT EXISTS resolved_at   TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_feedback_status ON feedback (status, created_at DESC);

UPDATE feedback
   SET project_id = (substring(route from '/projects/([0-9a-fA-F-]{36})'))::uuid
 WHERE project_id IS NULL
   AND route ~ '/projects/[0-9a-fA-F-]{36}';

UPDATE feedback
   SET app_name = substring(route from '/apps/([^/?#]+)')
 WHERE app_name = ''
   AND route ~ '/apps/[^/?#]+';

-- Explicit grants. 033_regrant_dada_all_tables.sql records why default
-- privileges cannot be relied on here.
GRANT SELECT, INSERT, UPDATE, DELETE ON feedback TO dada;
