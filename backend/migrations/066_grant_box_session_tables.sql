-- 066_grant_box_session_tables.sql
-- Explicit GRANT on the slice-2 box tables to the dada app role.
--
-- Same reasoning as 062, and it is not redundant with 005/033: those grant ON ALL
-- TABLES at the moment they run, and ALTER DEFAULT PRIVILEGES only covers objects
-- created by the role named in it. A table created later by a different migration
-- role silently lacks dada access and the symptom surfaces far from the cause --
-- which is exactly how 030_default_domains broke the build-agent handoff and why
-- 033_regrant_dada_all_tables.sql had to exist. So every new box table gets its
-- own named grant in its own migration, right after the table is created.

GRANT SELECT, INSERT, UPDATE, DELETE ON box_sessions TO dada;
GRANT SELECT, INSERT, UPDATE, DELETE ON box_attachments TO dada;
GRANT SELECT, INSERT, UPDATE, DELETE ON box_exposures TO dada;
GRANT SELECT, INSERT, UPDATE, DELETE ON box_crystallizations TO dada;

-- No SERIAL columns in any of the four (UUID PK, gen_random_uuid default). Kept as
-- a no-op safety net so a future SERIAL does not need anybody to remember this
-- file exists.
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO dada;
