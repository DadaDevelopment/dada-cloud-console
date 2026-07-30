-- 062_grant_box_tables.sql
-- Explicit GRANT on the box tables to the dada app role.
--
-- Not redundant with 005/033: those grant ON ALL TABLES at the time they run,
-- and ALTER DEFAULT PRIVILEGES only covers objects created by the role named in
-- it. A table created later by a different migration role silently lacks dada
-- access, and the symptom surfaces far from the cause -- that is exactly how
-- 030_default_domains broke the build-agent's deploy handoff and why
-- 033_regrant_dada_all_tables.sql exists. So every new box table gets its own
-- named grant, in its own migration, right after the table is created.
--
-- Named per table rather than ON ALL TABLES so this file is a readable record of
-- which tables the box feature added.

GRANT SELECT, INSERT, UPDATE, DELETE ON boxes TO dada;

-- boxes has no sequences today (UUID PK, gen_random_uuid default). The
-- sequence grant is kept as a no-op safety net for a future SERIAL column added
-- to a box table, so nobody has to remember this file exists.
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO dada;
