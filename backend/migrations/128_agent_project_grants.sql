-- 128_agent_project_grants.sql
--
-- A machine identity (Keycloak group /agents) holds only the project grants it
-- was handed: jwt.go deliberately denies it a personal org, and permissions.go
-- refuses to cascade an org role into a project for it. That carve-out is what
-- makes an agent token safe to hand to an automated run -- and it is also why,
-- until now, there was no way to hand one a project at all. The only lever that
-- existed was a human token, and a human token here carries /platform-admins:
-- Owner on every project of every tenant.
--
-- This table is the missing grant. It is read ONLY for callers that are already
-- in /agents (see effectiveRole), so a row naming a human is inert and cannot
-- become a second path to elevate a person outside member management.
--
-- expires_at is NOT NULL on purpose. A run can die without ever calling finish,
-- and a grant that only a finish call revokes would outlive the work it was for.
-- Revocation is therefore two independent things: revoked_at (the finish call)
-- and expires_at (the clock). Rows are never deleted -- who granted what, on
-- which project, for which run, and when it ended is the audit trail.
CREATE TABLE IF NOT EXISTS agent_project_grants (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id    UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    agent_user_id UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role          TEXT        NOT NULL,
    run_ref       TEXT        NOT NULL DEFAULT '',
    granted_by    UUID        REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    revoked_at    TIMESTAMPTZ
);

-- The hot path is one lookup per request on a project-scoped route: "does this
-- agent hold a live grant on this project". Partial index so the ended rows the
-- table keeps for audit never widen it.
CREATE INDEX IF NOT EXISTS agent_project_grants_live_idx
    ON agent_project_grants (agent_user_id, project_id)
    WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS agent_project_grants_project_idx
    ON agent_project_grants (project_id, created_at DESC);
