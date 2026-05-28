-- 004_vm_track.sql
-- VM track: app_servers table + environments runtime/app_server_id columns

-- ① Create app_servers FIRST (environments will FK to it).
-- Prod can already contain these objects from an earlier bootstrap while
-- schema_migrations is missing this version. In that drift state the app role
-- may not own the table, so no-op when the desired object already exists.
DO $$
BEGIN
    CREATE TABLE app_servers (
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
EXCEPTION
    WHEN duplicate_table THEN
        NULL;
    WHEN insufficient_privilege THEN
        IF to_regclass('public.app_servers') IS NULL THEN
            RAISE;
        END IF;
END;
$$;

DO $$
BEGIN
    IF to_regclass('public.idx_app_servers_project') IS NULL THEN
        CREATE INDEX idx_app_servers_project ON app_servers(project_id);
    END IF;
EXCEPTION
    WHEN insufficient_privilege THEN
        IF to_regclass('public.idx_app_servers_project') IS NULL THEN
            RAISE;
        END IF;
END;
$$;

-- ② Extend environments with runtime + optional server ref
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = 'environments' AND column_name = 'runtime'
    ) THEN
        ALTER TABLE environments
            ADD COLUMN runtime VARCHAR(20) NOT NULL DEFAULT 'k8s'
                CHECK (runtime IN ('k8s', 'vm'));
    END IF;
EXCEPTION
    WHEN insufficient_privilege THEN
        IF NOT EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'public' AND table_name = 'environments' AND column_name = 'runtime'
        ) THEN
            RAISE;
        END IF;
END;
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = 'environments' AND column_name = 'app_server_id'
    ) THEN
        ALTER TABLE environments
            ADD COLUMN app_server_id UUID REFERENCES app_servers(id) ON DELETE SET NULL;
    END IF;
EXCEPTION
    WHEN insufficient_privilege THEN
        IF NOT EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'public' AND table_name = 'environments' AND column_name = 'app_server_id'
        ) THEN
            RAISE;
        END IF;
END;
$$;
