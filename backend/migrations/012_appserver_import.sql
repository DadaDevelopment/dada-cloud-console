-- 012_appserver_import.sql
-- beget-reader reverse-sync: allow VMs discovered in Beget and adopted into the
-- platform. Extends the app_servers.source and app_servers.status CHECK
-- constraints with 'beget-import' / 'Imported'.
-- Idempotent + privilege-tolerant, matching the 004/009 drift-handling style.

DO $$
BEGIN
    ALTER TABLE app_servers DROP CONSTRAINT IF EXISTS app_servers_source_check;
    ALTER TABLE app_servers ADD CONSTRAINT app_servers_source_check
        CHECK (source IN ('terraform', 'manual', 'beget-import'));
EXCEPTION
    WHEN insufficient_privilege THEN
        NULL;
END;
$$;

DO $$
BEGIN
    ALTER TABLE app_servers DROP CONSTRAINT IF EXISTS app_servers_status_check;
    ALTER TABLE app_servers ADD CONSTRAINT app_servers_status_check
        CHECK (status IN ('Provisioning','WaitingForAgent','Ready',
                          'Deleting','Deleted','Failed','Imported'));
EXCEPTION
    WHEN insufficient_privilege THEN
        NULL;
END;
$$;
