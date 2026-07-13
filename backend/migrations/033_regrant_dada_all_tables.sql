-- 033_regrant_dada_all_tables.sql
-- Re-grant the dada app user (build-agent's DB role) full DML on every table
-- and sequence in the schema, and re-assert default privileges for the current
-- migration role.
--
-- Why: 005_grant_dada_user granted dada on the tables that existed then and set
-- default privileges FOR ROLE current_user. When a later table is created by a
-- role whose default privileges do not include dada (e.g. domain_hostnames from
-- 030_default_domains), dada silently lacks access. That broke the build-agent's
-- deploy handoff: a successful build inserts into domain_hostnames (surrogate
-- default domain) inside the deploy transaction, hit "permission denied for
-- table domain_hostnames", rolled the whole transaction back, and the app stayed
-- NotDeployed with no operation enqueued. Re-granting on ALL TABLES catches
-- domain_hostnames and any other post-005 table that slipped through.

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO dada;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO dada;

DO $$
BEGIN
  EXECUTE format(
    'ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO dada',
    current_user
  );
  EXECUTE format(
    'ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO dada',
    current_user
  );
END
$$;
