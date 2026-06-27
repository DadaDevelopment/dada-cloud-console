-- 025_cloud_tasks.sql
-- DadaAgent cloud-task integration: one row per fired task.
-- A task is imperative (runs on the agent), NOT in the operations/gitops machine.
-- Forward-only, additive, idempotent.

CREATE TABLE IF NOT EXISTS cloud_tasks (
    id             UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id     UUID         NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_id UUID         NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    app_name       VARCHAR(255) NOT NULL,
    git_repo_id    UUID         REFERENCES git_repos(id) ON DELETE SET NULL,
    task_type      VARCHAR(100) NOT NULL,
    intent_id      VARCHAR(255),
    workflow_id    VARCHAR(255),
    status         VARCHAR(20)  NOT NULL DEFAULT 'running'
                   CHECK (status IN ('running','completed','failed','canceled')),
    pr_url         VARCHAR(1000),
    artifacts      JSONB        NOT NULL DEFAULT '[]',
    error          TEXT,
    actor_id       UUID         NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cloud_tasks_project_app
    ON cloud_tasks(project_id, app_name);
CREATE INDEX IF NOT EXISTS idx_cloud_tasks_running
    ON cloud_tasks(status) WHERE status = 'running';
CREATE UNIQUE INDEX IF NOT EXISTS idx_cloud_tasks_intent
    ON cloud_tasks(intent_id) WHERE intent_id IS NOT NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON cloud_tasks TO dada;
