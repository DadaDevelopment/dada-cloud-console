-- 087_service_identity.sql
--
-- ADR-021. One platform principal per app, owning one revocable token that
-- every platform service accepts, with scopes deciding what it may do.
--
-- Why a surrogate id instead of the (app_name, environment_id) coordinate:
-- environments are project-scoped (001: project_id FK, UNIQUE(project_id,
-- name)) and MoveApp assigns the app a DIFFERENT environment row in the
-- destination project (gitops-agent/internal/worker/move_app.go, TargetEnvID).
-- So both project_id and environment_id move with the app. An identity keyed
-- by either would be lost by the very operation this table exists to survive
-- -- which is exactly how reels-tracker lost its AI key on 2026-08-02:
-- ai_gateway_keys.project_id ON DELETE CASCADE (058) took the key when the
-- source project was deleted after the move.
--
-- Hence: id is the principal and never changes; project_id/environment_id/
-- app_name are WHERE the identity currently lives and are re-pointed by
-- MoveApp in the same transaction as repointMovedAppSnapshots (ADR-014 step 7).
-- The token names identity_id, so a move re-points a row and re-renders a
-- namespace -- it never re-mints, revokes, redelivers or restarts anything.
--
-- Location columns are NULLable on purpose. The payment gateway's first caller
-- is a Telegram bot on a bare VPS with no namespace and no App snapshot (083).
-- It gets an identity row with no location and no CR: revealed once at mint
-- time, configured by hand. It gains revocation, scopes and attribution; it
-- does not gain automatic delivery, because there is nothing to deliver into.
--
-- Two tables, not one, so rotation is an INSERT plus a revoke and leaves the
-- identity -- and everything attributed to it -- untouched.
--
-- ai_gateway_keys (058) and pay_service_keys (083) gain a nullable identity_id
-- so both can be read through an identity before either drops its own owner
-- column. Nothing is dropped here: 058's project_id stays NOT NULL until the
-- introspection path no longer reads it, in its own later migration.
CREATE TABLE IF NOT EXISTS service_identities (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    app_name       TEXT,
    project_id     UUID        REFERENCES projects(id) ON DELETE SET NULL,
    environment_id UUID        REFERENCES environments(id) ON DELETE SET NULL,
    display_name   TEXT        NOT NULL,
    scopes         TEXT        NOT NULL DEFAULT 'ai:chat ai:embeddings',
    created_by     UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at     TIMESTAMPTZ
);

-- One live identity per app instance. Partial, so a revoked identity does not
-- block re-declaring the app, and so the location-less (VPS) rows -- which all
-- have NULL app_name -- never collide with each other.
CREATE UNIQUE INDEX IF NOT EXISTS uq_service_identities_app_env
    ON service_identities (app_name, environment_id)
    WHERE revoked_at IS NULL AND app_name IS NOT NULL AND environment_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_service_identities_project
    ON service_identities (project_id, environment_id);

CREATE TABLE IF NOT EXISTS service_identity_tokens (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    identity_id  UUID        NOT NULL REFERENCES service_identities(id) ON DELETE CASCADE,
    token_hash   TEXT        NOT NULL UNIQUE,
    token_prefix TEXT        NOT NULL,
    created_by   UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_service_identity_tokens_identity
    ON service_identity_tokens (identity_id)
    WHERE revoked_at IS NULL;

DO $$
BEGIN
  IF to_regclass('public.ai_gateway_keys') IS NOT NULL THEN
    ALTER TABLE ai_gateway_keys
      ADD COLUMN IF NOT EXISTS identity_id UUID REFERENCES service_identities(id) ON DELETE CASCADE;
  END IF;
  IF to_regclass('public.pay_service_keys') IS NOT NULL THEN
    ALTER TABLE pay_service_keys
      ADD COLUMN IF NOT EXISTS identity_id UUID REFERENCES service_identities(id) ON DELETE CASCADE;
  END IF;
END $$;
