-- Composio integrations: one-click third-party authorization for a project's
-- end users, brokered by composio-broker (backend/cmd/composio-broker).
--
-- Why a table and not git: a Composio session's MCP URL is minted per end user
-- at runtime, and a RemoteMCPServer CR is a static, cluster-global object. One
-- CR per end user is not a design, it is a name fight in a shared namespace --
-- so the agent points at ONE broker URL per project and the per-user part lives
-- here. Same posture tg_bindings has toward tg-gateway: one owning service, the
-- console proxies through its internal API.
--
-- No FK to projects(id): the broker runs with its own (narrower) DB role, the
-- same split tg_bindings and the telemetry gateway already use.

-- composio_sessions: one tool-router session per (project, end user). Composio
-- sessions persist server-side and do not expire, so the mapping is ours to own
-- and ours to clean up. toolkits mirrors the session's server-side allowlist:
-- the broker PATCHes the session when a user connects one more app, and a
-- toolkit that is not in this list is invisible to the agent no matter what it
-- asks for -- that is the authorization gate, enforced at Composio, not by a
-- prompt.
CREATE TABLE IF NOT EXISTS composio_sessions (
    project_id   UUID        NOT NULL,
    end_user_key TEXT        NOT NULL,
    user_id      TEXT        NOT NULL,
    session_id   TEXT        NOT NULL,
    mcp_url      TEXT        NOT NULL,
    toolkits     TEXT[]      NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (project_id, end_user_key)
);

-- composio_integrations: what this end user authorized, and where it stands.
-- status mirrors Composio's connected-account status verbatim (INITIALIZING,
-- ACTIVE, EXPIRED, ...) rather than a bool: a link that was minted and never
-- finished is a real state, and calling it "connected" would offer an agent
-- tools that answer 401.
CREATE TABLE IF NOT EXISTS composio_integrations (
    project_id           UUID        NOT NULL,
    end_user_key         TEXT        NOT NULL,
    toolkit              TEXT        NOT NULL,
    connected_account_id TEXT        NOT NULL DEFAULT '',
    status               TEXT        NOT NULL DEFAULT 'INITIALIZING',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (project_id, end_user_key, toolkit)
);

CREATE INDEX IF NOT EXISTS idx_composio_integrations_project
    ON composio_integrations (project_id);

-- composio_tool_calls: the audit the MCP transport takes away from us. Over MCP
-- the client executes against Composio directly, so the SDK's before/after
-- execute hooks never run; the only place a tool call can be recorded is the
-- broker it passes through. Kept narrow on purpose: who, which tool, when,
-- never arguments.
CREATE TABLE IF NOT EXISTS composio_tool_calls (
    id           BIGSERIAL   PRIMARY KEY,
    project_id   UUID        NOT NULL,
    end_user_key TEXT        NOT NULL,
    agent_name   TEXT        NOT NULL DEFAULT '',
    tool_name    TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_composio_tool_calls_project_time
    ON composio_tool_calls (project_id, created_at DESC);

GRANT SELECT, INSERT, UPDATE, DELETE ON composio_sessions TO dada;
GRANT SELECT, INSERT, UPDATE, DELETE ON composio_integrations TO dada;
GRANT SELECT, INSERT, UPDATE, DELETE ON composio_tool_calls TO dada;
GRANT USAGE, SELECT ON SEQUENCE composio_tool_calls_id_seq TO dada;
