-- An agent's prompt and skills come from a directory in the client's git
-- repository (docs/agent-prompt-source.md) instead of being pasted into
-- saveAgent and into the console values file by hand. One row per agent
-- names the repository, the branch and the directory, and holds the console's
-- copy of what was last synced: the prompt goes into the ManagedAgent claim
-- through the operations queue, the skills are served to the agent runtime
-- straight from this row.
--
-- There is no agents table to extend: an agent is a resource_snapshots row
-- mirroring the claim in git, so the source keys on the same triple.
CREATE TABLE IF NOT EXISTS agent_prompt_sources (
    project_id       UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_id   UUID NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    agent_name       VARCHAR(63) NOT NULL,
    installation_id  UUID NOT NULL REFERENCES git_app_installations(id) ON DELETE CASCADE,
    repo_full_name   TEXT NOT NULL,
    ref              TEXT NOT NULL DEFAULT 'main',
    path             TEXT NOT NULL,
    resolved_sha     TEXT NOT NULL DEFAULT '',
    synced_at        TIMESTAMPTZ,
    last_checked_at  TIMESTAMPTZ,
    last_sync_status TEXT NOT NULL DEFAULT 'pending',
    last_sync_error  TEXT NOT NULL DEFAULT '',
    prompt           TEXT NOT NULL DEFAULT '',
    prompt_title     TEXT NOT NULL DEFAULT '',
    prompt_version   TEXT NOT NULL DEFAULT '',
    skills           JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by       UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (project_id, environment_id, agent_name),
    CONSTRAINT agent_prompt_sources_status_chk
        CHECK (last_sync_status IN ('pending', 'ok', 'error'))
);

CREATE INDEX IF NOT EXISTS idx_agent_prompt_sources_agent_name
    ON agent_prompt_sources (agent_name);
