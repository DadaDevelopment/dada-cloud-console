-- 030_default_domains.sql
-- Default (surrogate) domains: every app gets an auto hostname
-- "<app>-<rand>.<base>" at create time so it is immediately reachable
-- (Vercel-style temp URL). These rows live in domain_hostnames alongside the
-- user-owned custom domains, marked managed=true. They have NO domain_authorization
-- (the base zone is platform-owned, so there is no TXT ownership proof) -- hence
-- authorization_id becomes nullable.

ALTER TABLE domain_hostnames
    ALTER COLUMN authorization_id DROP NOT NULL;

ALTER TABLE domain_hostnames
    ADD COLUMN IF NOT EXISTS managed BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_domain_hostnames_managed
    ON domain_hostnames(managed);
