-- 015_custom_domains.sql
-- User-owned custom domains + automatic TLS, Vercel-style two-level model.
--
-- Level 1 (domain_authorizations): a project proves ownership of an apex domain ONCE via a TXT
-- challenge. Once verified, the project may attach that apex AND any of its subdomains to its
-- deployments without re-verifying.
--
-- Level 2 (domain_hostnames): a specific hostname (the apex or a subdomain) is attached to one
-- app/environment. gitops-agent renders a native k8s Ingress (ingressClassName: nginx +
-- cert-manager.io/cluster-issuer: letsencrypt-prod) for it; cert-manager issues a per-host LE cert
-- via HTTP-01. No Beget / PublicApi is involved — external zones are owned by the user.

CREATE TABLE IF NOT EXISTS domain_authorizations (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id         UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    -- Registrable apex, e.g. "acme.com". Global uniqueness is the anti-hijack gate: one apex can be
    -- owned by at most one project across the whole platform.
    apex_domain        VARCHAR(253) NOT NULL UNIQUE,
    -- High-entropy token placed in the TXT value (dada-domain-verify=<token>). Unique per row, so
    -- only whoever controls the zone can produce the matching record.
    verification_token VARCHAR(128) NOT NULL,
    status             VARCHAR(20)  NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','verified','failed')),
    verified_at        TIMESTAMPTZ,
    last_checked_at    TIMESTAMPTZ,
    error_message      TEXT,
    created_by         UUID NOT NULL REFERENCES users(id),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_domain_authorizations_project
    ON domain_authorizations(project_id);
CREATE INDEX IF NOT EXISTS idx_domain_authorizations_status
    ON domain_authorizations(status);

CREATE TABLE IF NOT EXISTS domain_hostnames (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    authorization_id UUID NOT NULL REFERENCES domain_authorizations(id) ON DELETE CASCADE,
    environment_id   UUID NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    app_name         VARCHAR(255) NOT NULL,
    -- Apex or subdomain under the authorized apex. Global uniqueness: one hostname routes to at most
    -- one deployment.
    hostname         VARCHAR(253) NOT NULL UNIQUE,
    -- What the user must point at our LB: 'A' for the apex (LB IP), 'CNAME' for subdomains.
    record_type      VARCHAR(8)   NOT NULL CHECK (record_type IN ('A','CNAME')),
    status           VARCHAR(20)  NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','active','failed')),
    cert_status      VARCHAR(20)  NOT NULL DEFAULT 'pending',
    operation_id     UUID REFERENCES operations(id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_domain_hostnames_authorization
    ON domain_hostnames(authorization_id);
CREATE INDEX IF NOT EXISTS idx_domain_hostnames_env_app
    ON domain_hostnames(environment_id, app_name);
