-- 013_git_build_deploy.sql
-- Vercel-flow: git repos, GitHub App installations, builds, build logs,
-- deployments, and per-app environment variables.
-- Forward-only, additive. Idempotent (IF NOT EXISTS throughout).

-- ① git_app_installations
-- One row per GitHub App installation, bound to a project.
-- GitHub App token is NOT stored here (minted per-build, ~1h TTL).
CREATE TABLE IF NOT EXISTS git_app_installations (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      UUID         NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    provider        VARCHAR(20)  NOT NULL CHECK (provider IN ('github', 'gitlab')),
    installation_id BIGINT       NOT NULL,          -- GitHub: installation id
    account_login   VARCHAR(255) NOT NULL,          -- GitHub org/user slug
    account_type    VARCHAR(20)  NOT NULL,           -- 'Organization' | 'User'
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE(project_id, provider, installation_id)
);

CREATE INDEX IF NOT EXISTS idx_git_app_installations_project
    ON git_app_installations(project_id);

-- ② git_repos
-- One row per (project, environment, app_name) git-linked application.
-- GitHub: no token stored (App install token minted per-build).
-- GitLab: token_encrypted (AES-GCM, key = GITOPS_ENCRYPTION_KEY).
CREATE TABLE IF NOT EXISTS git_repos (
    id                  UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id          UUID         NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_id      UUID         NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    app_name            VARCHAR(255) NOT NULL,
    installation_id     UUID         REFERENCES git_app_installations(id) ON DELETE SET NULL,
    provider            VARCHAR(20)  NOT NULL CHECK (provider IN ('github', 'gitlab')),
    repo_full_name      VARCHAR(500) NOT NULL,      -- 'org/repo'
    clone_url           VARCHAR(500) NOT NULL,
    token_encrypted     BYTEA,                      -- GitLab only; NULL for GitHub App
    webhook_secret      VARCHAR(255),               -- per-repo HMAC secret
    production_branch   VARCHAR(255) NOT NULL DEFAULT 'main',
    root_dir            VARCHAR(500) NOT NULL DEFAULT '.',
    framework_override  VARCHAR(100),               -- force nixpacks provider; NULL = auto-detect
    auto_deploy         BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE(project_id, environment_id, app_name)
);

CREATE INDEX IF NOT EXISTS idx_git_repos_project
    ON git_repos(project_id);
CREATE INDEX IF NOT EXISTS idx_git_repos_repo_full_name
    ON git_repos(repo_full_name);

-- ③ builds
-- One row per triggered build attempt. Idempotent on (git_repo_id, commit_sha)
-- so duplicate webhooks are safely ignored.
CREATE TABLE IF NOT EXISTS builds (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    git_repo_id     UUID         NOT NULL REFERENCES git_repos(id) ON DELETE CASCADE,
    environment_id  UUID         NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    app_name        VARCHAR(255) NOT NULL,
    commit_sha      VARCHAR(40)  NOT NULL,
    commit_message  TEXT,
    branch          VARCHAR(255) NOT NULL,
    triggered_by    UUID         REFERENCES users(id),        -- NULL = system/webhook
    trigger         VARCHAR(20)  NOT NULL DEFAULT 'push'
                    CHECK (trigger IN ('push', 'pr', 'manual', 'rollback')),
    status          VARCHAR(20)  NOT NULL DEFAULT 'queued'
                    CHECK (status IN ('queued', 'detecting', 'building', 'pushing',
                                      'success', 'failed', 'canceled')),
    image_uri       VARCHAR(500),                             -- set on success
    logs_ref        VARCHAR(500),                             -- object-store key (gzipped)
    error_message   TEXT,
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE(git_repo_id, commit_sha)
);

CREATE INDEX IF NOT EXISTS idx_builds_environment_id
    ON builds(environment_id);
CREATE INDEX IF NOT EXISTS idx_builds_git_repo_id
    ON builds(git_repo_id);
CREATE INDEX IF NOT EXISTS idx_builds_status
    ON builds(status);
CREATE INDEX IF NOT EXISTS idx_builds_created_at
    ON builds(created_at DESC);
-- Partial index for the poller: only live rows appear in this index.
CREATE INDEX IF NOT EXISTS idx_builds_queued
    ON builds(created_at)
    WHERE status = 'queued';

-- ④ builds_logs
-- Live/recent log frames from the build-agent log streamer.
-- On build terminal state: gzip → object store → prune these rows.
CREATE TABLE IF NOT EXISTS builds_logs (
    id         BIGSERIAL    PRIMARY KEY,
    build_id   UUID         NOT NULL REFERENCES builds(id) ON DELETE CASCADE,
    seq        INTEGER      NOT NULL,   -- monotone frame counter within build
    line       TEXT         NOT NULL,
    written_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_builds_logs_build_id_seq
    ON builds_logs(build_id, seq);

-- ⑤ deployments
-- Immutable record of each deploy event. image_uri is denormalized at write time
-- so rollbacks need no back-reference to the build row.
CREATE TABLE IF NOT EXISTS deployments (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    environment_id  UUID         NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    app_name        VARCHAR(255) NOT NULL,
    build_id        UUID         REFERENCES builds(id) ON DELETE SET NULL,
    image_uri       VARCHAR(500) NOT NULL,          -- immutable: harbor.../project/app@sha256:...
    operation_id    UUID         REFERENCES operations(id),  -- DeployImageVersion op
    trigger         VARCHAR(20)  NOT NULL DEFAULT 'push'
                    CHECK (trigger IN ('push', 'pr', 'manual', 'rollback', 'promote')),
    is_current      BOOLEAN      NOT NULL DEFAULT FALSE,
    deployed_by     UUID         REFERENCES users(id),       -- NULL = system
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_deployments_environment_id
    ON deployments(environment_id);
CREATE INDEX IF NOT EXISTS idx_deployments_build_id
    ON deployments(build_id);
CREATE INDEX IF NOT EXISTS idx_deployments_operation_id
    ON deployments(operation_id);
-- Guarantees at most one current deployment per (env, app) at all times.
CREATE UNIQUE INDEX IF NOT EXISTS idx_deployments_current
    ON deployments(environment_id, app_name)
    WHERE is_current;

-- ⑥ env_vars
-- Per-(environment, app_name, key) variable. Sensitive values stored AES-GCM
-- encrypted (key = GITOPS_ENCRYPTION_KEY). API never returns plaintext for
-- is_secret=true entries.
CREATE TABLE IF NOT EXISTS env_vars (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    environment_id  UUID         NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    app_name        VARCHAR(255) NOT NULL,
    key             VARCHAR(255) NOT NULL,
    value_encrypted BYTEA        NOT NULL,          -- AES-GCM; always encrypted, even non-secret
    is_secret       BOOLEAN      NOT NULL DEFAULT FALSE,
    scope           VARCHAR(10)  NOT NULL DEFAULT 'runtime'
                    CHECK (scope IN ('build', 'runtime', 'both')),
    created_by      UUID         REFERENCES users(id),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE(environment_id, app_name, key)
);

CREATE INDEX IF NOT EXISTS idx_env_vars_environment_app
    ON env_vars(environment_id, app_name);

-- ⑦ Grants (mirrors 006_ai_studio.sql pattern)
GRANT SELECT, INSERT, UPDATE, DELETE ON
    git_app_installations,
    git_repos,
    builds,
    builds_logs,
    deployments,
    env_vars
TO dada;

-- builds_logs uses a BIGSERIAL; grant usage on the sequence too.
GRANT USAGE, SELECT ON SEQUENCE builds_logs_id_seq TO dada;
