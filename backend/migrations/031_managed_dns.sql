-- 031_managed_dns.sql
-- Managed DNS via NS delegation (design: docs/plans/2026-07-13-ns-delegation-managed-dns.md).
--
-- A verified apex authorization can pick one of two delegation modes:
--   'record'    (default) - the user points an A/CNAME record at our LB themselves.
--   'delegated'           - the user delegates the whole zone to our nameservers
--                           (NS -> ns1/ns2.dada-tuda.ru) and we manage the zone in
--                           PowerDNS on their behalf (apex/www routing + record editor).
--
-- managed_zones tracks one PowerDNS zone per delegated apex. Records themselves are
-- read/written live through the PowerDNS API (source of truth = PowerDNS), not mirrored
-- here, to avoid drift. status flips awaiting_ns -> active once the delegation poller sees
-- our nameservers on the apex's NS set.

ALTER TABLE domain_authorizations
    ADD COLUMN IF NOT EXISTS delegation_mode VARCHAR(16) NOT NULL DEFAULT 'record'
        CHECK (delegation_mode IN ('record','delegated'));

CREATE TABLE IF NOT EXISTS managed_zones (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    authorization_id UUID NOT NULL REFERENCES domain_authorizations(id) ON DELETE CASCADE,
    -- Registrable apex the zone is authoritative for, e.g. "acme.com". Globally unique:
    -- one apex maps to at most one managed zone across the platform.
    apex             VARCHAR(253) NOT NULL UNIQUE,
    -- PowerDNS zone id (the apex with a trailing dot, e.g. "acme.com.").
    pdns_zone        TEXT NOT NULL,
    status           VARCHAR(20) NOT NULL DEFAULT 'awaiting_ns'
                     CHECK (status IN ('awaiting_ns','active','failed')),
    last_checked_at  TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_managed_zones_authorization
    ON managed_zones(authorization_id);
CREATE INDEX IF NOT EXISTS idx_managed_zones_status
    ON managed_zones(status);
