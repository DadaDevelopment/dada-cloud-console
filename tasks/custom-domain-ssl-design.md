# Custom domains + automatic TLS — design

Status: approved model (2026-06-20). Supersedes the earlier "reuse PublicApi" draft, which was wrong:
PublicApi only manages Beget DNS A-records in the `dada-tuda.ru` zone — it cannot touch user-owned
external zones, and external domains must NOT involve Beget at all.

## Goal

Any tenant attaches THEIR OWN domain (`acme.com`, `shop.acme.com`) to a deployed app, proves
ownership, and gets an auto-issued + auto-renewed TLS cert. 1-to-1 with how Vercel / Cloudflare-for-SaaS
do it.

## The model: two levels (Vercel-style)

### Level 1 — Authorize an apex domain (once, per project)

- User adds `acme.com` in the console.
- Console returns a TXT challenge: record `_dada-verify.acme.com`, value `dada-domain-verify=<token>`.
- User adds it at THEIR registrar.
- A backend poller resolves the TXT. On match, the **apex `acme.com` becomes authorized for that project**.
- After that, the project may attach `acme.com` AND any subdomain (`shop.acme.com`, `api.acme.com`, …)
  to any of its deployments — no re-verification per hostname. This is the Vercel domains /
  Cloudflare custom-hostnames pattern: prove the apex once, use the whole tree.

**Scope = per-project** (confirmed). An apex is owned by exactly one project (global unique on
`apex_domain`). A second project claiming the same apex must pass its own TXT challenge (different
token) — impossible without controlling the zone. This is the anti-hijack gate.

### Level 2 — Attach a hostname to a deployment

- User picks an authorized apex → types a hostname (apex itself or a subdomain) → picks app + env.
- Backend checks the hostname's registrable parent is an authorized apex **for this project**. If not,
  reject (second anti-hijack check — you can only attach under a domain you own).
- Console shows the routing record the user must add at their registrar:
  - apex `acme.com` → `A <CUSTOM_DOMAIN_A_TARGET>` (the ingress-nginx-pub LB IP)
  - subdomain `shop.acme.com` → `CNAME <CUSTOM_DOMAIN_CNAME_TARGET>` (stable hostname → LB)
- Backend enqueues an `AttachCustomHostname` operation. gitops-agent injects a **native k8s Ingress**
  into the owning app's `resources.values.yaml` manifests list:
  - `ingressClassName: nginx`
  - annotation `cert-manager.io/cluster-issuer: letsencrypt-prod`
  - `tls:` block → per-host secret
- Once the user's DNS points at our LB, cert-manager solves HTTP-01 and issues a per-host Let's Encrypt
  cert. Auto-renewed by cert-manager. No Beget, no PublicApi.

Detach = `DetachCustomHostname` op → removes that Ingress entry from the manifests list →
cert-manager GCs the cert/secret.

## Why native Ingress (not PublicApi)

- PublicApi composition = `publicapi-beget-dns`: it only writes A-records into the Beget-hosted
  `dada-tuda.ru` zone via the Beget API. Useless for `acme.com` (Beget doesn't own that zone).
- The cert + routing for platform apps (n8n etc.) already use a plain Ingress with
  `cert-manager.io/cluster-issuer: letsencrypt-prod` + HTTP-01. We replicate exactly that, per
  attached hostname, via the `helm/app-resources` manifests passthrough (`all.yaml` ranges over
  `.Values.manifests`). Any host pointing at our LB gets a cert. Mechanism already proven in prod.

## Data model (migration 015, two tables)

```
domain_authorizations            -- Level 1, the ownership gate
  id                 uuid pk
  project_id         uuid fk projects(id) on delete cascade
  apex_domain        varchar(253) UNIQUE        -- registrable apex, e.g. acme.com
  verification_token varchar(128)               -- random; goes in the TXT value
  status             pending|verified|failed
  verified_at        timestamptz
  last_checked_at    timestamptz
  error_message      text
  created_by         uuid fk users(id)
  created_at/updated_at

domain_hostnames                 -- Level 2, the attachments
  id              uuid pk
  authorization_id uuid fk domain_authorizations(id) on delete cascade
  environment_id  uuid fk environments(id) on delete cascade
  app_name        varchar(255)
  hostname        varchar(253) UNIQUE           -- apex or subdomain, global unique
  record_type     A|CNAME                       -- what the user must point
  status          pending|active|failed
  cert_status     varchar(20)
  operation_id    uuid fk operations(id)
  created_at/updated_at
```

`domain_hostnames.hostname` is globally unique → one hostname routes to at most one deployment.
The authorization FK + the per-project apex uniqueness make hijack impossible without DNS control.

## Backend API (project-scoped, under existing authMiddleware + RBAC)

```
POST   /api/v1/projects/:projectId/domain-authorizations              add apex → returns TXT challenge
GET    /api/v1/projects/:projectId/domain-authorizations              list (with status)
POST   /api/v1/projects/:projectId/domain-authorizations/:id/verify   force a verify poll now
DELETE /api/v1/projects/:projectId/domain-authorizations/:id          remove (cascades hostnames)

POST   /api/v1/projects/:projectId/environments/:envId/apps/:appName/hostnames   attach
GET    /api/v1/projects/:projectId/environments/:envId/apps/:appName/hostnames   list
DELETE /api/v1/projects/:projectId/environments/:envId/apps/:appName/hostnames/:id   detach
```

Writes require `canWrite` (project role), mirroring the endpoints handler. Attach validates the
hostname's apex is a `verified` authorization for THIS project before enqueuing.

## DNS verification poller

Background goroutine in the backend (like other pollers). Every N seconds:
- pending/failed authorizations: resolve TXT `_dada-verify.<apex>`; on `dada-domain-verify=<token>`
  match → status `verified`, set `verified_at`. Update `last_checked_at` always.
- Uses Go `net.LookupTXT`. No external dependency. Anti-hijack: token is high-entropy and unique per
  authorization row, so the TXT can only be produced by whoever controls the zone.

## gitops-agent

- New op `AttachCustomHostname`: render a native Ingress (`RenderCustomIngress`) and upsert it into the
  app's `resources.values.yaml` manifests list (keyed `Ingress`/`<host-as-name>`). Commit → Argo →
  cert-manager.
- New op `DetachCustomHostname`: remove that `{Ingress, name}` entry.
- Reuses existing `upsertManifestFile` / `removeManifestsFile` / `AppResourcesValuesGitPath`.

## Out of MVP

- Wildcard `*.acme.com`: needs DNS-01 (nameserver delegation), which Vercel also requires an NS
  handover for. Not built now. MVP = explicit apex + named subdomains via HTTP-01.
- Apex via CNAME flattening / ALIAS: we tell apex users to use an A record to the LB IP. (CNAME at
  apex is invalid DNS; ALIAS/ANAME is registrar-specific.)
