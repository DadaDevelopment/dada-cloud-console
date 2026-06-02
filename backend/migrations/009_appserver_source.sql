-- 009_appserver_source.sql
-- Manual VM connect: distinguish terraform-provisioned vs hand-connected app_servers.
-- Idempotent + privilege-tolerant, matching the 004 drift-handling style.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = 'app_servers' AND column_name = 'source'
    ) THEN
        ALTER TABLE app_servers
            ADD COLUMN source VARCHAR(20) NOT NULL DEFAULT 'terraform'
                CHECK (source IN ('terraform', 'manual'));
    END IF;
EXCEPTION
    WHEN insufficient_privilege THEN
        IF NOT EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'public' AND table_name = 'app_servers' AND column_name = 'source'
        ) THEN
            RAISE;
        END IF;
END;
$$;
