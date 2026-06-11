-- 011_keycloak_identity.sql
-- Adds the Keycloak/OIDC identity link to users so a Keycloak `sub` can map to a
-- local users.id (required because operations.actor_id, audit_events.actor_id and
-- project_members.user_id all FK -> users.id; M3 keeps those FKs intact and just
-- back-fills a users row per Keycloak identity).
--
-- Smallest-change decisions:
--   * keycloak_sub is a NULLABLE TEXT UNIQUE column. Existing local users keep
--     keycloak_sub = NULL; only OIDC-provisioned rows set it.
--   * password_hash is LEFT NOT NULL. OIDC users get password_hash = '' inserted
--     by the provisioner — they can never log in via /auth/login because the
--     bcrypt compare against an empty hash always fails. This avoids relaxing the
--     NOT NULL constraint (a wider, harder-to-reverse change) entirely.
--
-- Idempotent + privilege-tolerant, matching the 009 drift-handling style.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = 'users' AND column_name = 'keycloak_sub'
    ) THEN
        ALTER TABLE users ADD COLUMN keycloak_sub TEXT UNIQUE;
    END IF;
EXCEPTION
    WHEN insufficient_privilege THEN
        IF NOT EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'public' AND table_name = 'users' AND column_name = 'keycloak_sub'
        ) THEN
            RAISE;
        END IF;
END;
$$;

-- Explicit index on keycloak_sub. The UNIQUE constraint above already creates a
-- backing index, but we add an IF NOT EXISTS guard so re-runs on a DB where the
-- column pre-existed (without the unique index) still get a lookup index.
CREATE INDEX IF NOT EXISTS idx_users_keycloak_sub ON users(keycloak_sub);
