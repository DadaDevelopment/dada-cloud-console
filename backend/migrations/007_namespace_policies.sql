-- 007_namespace_policies.sql
-- Add limit_range and resource_quota columns to environments.
-- These mirror clusters/beget-prod/namespace-policies/<namespace>.yaml and are
-- kept in sync bidirectionally by the gitops-agent.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = 'environments' AND column_name = 'limit_range'
    ) THEN
        ALTER TABLE environments ADD COLUMN limit_range JSONB NOT NULL DEFAULT '{}';
    END IF;
END;
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = 'environments' AND column_name = 'resource_quota'
    ) THEN
        ALTER TABLE environments ADD COLUMN resource_quota JSONB NOT NULL DEFAULT '{}';
    END IF;
END;
$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON environments TO dada;
