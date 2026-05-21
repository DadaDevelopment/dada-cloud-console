-- 005_grant_dada_user.sql
-- Grant full DML access on all existing tables and sequences to the dada app user.
-- Also set default privileges so future objects created by postgres are also accessible.

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO dada;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO dada;

-- Ensure future tables/sequences created by postgres are auto-granted to dada.
ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO dada;
ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO dada;
