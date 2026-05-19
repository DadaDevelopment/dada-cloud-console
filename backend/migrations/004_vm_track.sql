-- 004_vm_track.sql
-- VM track: app_servers table + environments runtime/app_server_id columns

-- ① Create app_servers FIRST (environments will FK to it)
CREATE TABLE IF NOT EXISTS app_servers (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id          UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name                VARCHAR(255) NOT NULL,
    vm_ip               VARCHAR(45),
    vm_provider_id      VARCHAR(255),
    terraform_workspace VARCHAR(500),
    portainer_endpoint_id INTEGER,
    status              VARCHAR(50) NOT NULL DEFAULT 'Provisioning'
                        CHECK (status IN ('Provisioning','WaitingForAgent','Ready',
                                          'Deleting','Deleted','Failed')),
    error_message       TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(project_id, name)
);
CREATE INDEX IF NOT EXISTS idx_app_servers_project ON app_servers(project_id);

-- ② Extend environments with runtime + optional server ref
ALTER TABLE environments
    ADD COLUMN IF NOT EXISTS runtime VARCHAR(20) NOT NULL DEFAULT 'k8s'
        CHECK (runtime IN ('k8s', 'vm'));
ALTER TABLE environments
    ADD COLUMN IF NOT EXISTS app_server_id UUID REFERENCES app_servers(id) ON DELETE SET NULL;
