CREATE TABLE IF NOT EXISTS github_oauth_states (
    state       TEXT        PRIMARY KEY,
    project_id  UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id     UUID        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_github_oauth_states_created
    ON github_oauth_states(created_at);
