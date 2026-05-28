-- 005_grant_dada_user.sql
-- Grant full DML access on all existing tables and sequences to the dada app user.
-- Also set default privileges so future objects created by the migration runner
-- are auto-accessible. Uses current_user so it works regardless of the local
-- superuser name (postgres in prod, alex on dev machines, etc.).

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO dada;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO dada;

-- Ensure future tables/sequences created by the migration-running role are
-- auto-granted to dada.
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
