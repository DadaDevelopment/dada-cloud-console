# Managed Postgres -- external (public) endpoint

Status: DESIGN. Console scaffolding landed; infra + composition work is the blocker.

## Goal

Let a tenant opt a single managed database into a **public** connection endpoint,
so external clients (BI tools, local dev, another cloud) can connect -- without
exposing every tenant's database.

Decision (owner): **per-database, isolated**, exposed via **nginx-pub TCP 5432**.

## Current reality (verified live 2026-07-12)

- One **shared** Postgres: Service `postgresql` in namespace `databases`
  (`postgresql.databases.svc.cluster.local`, ClusterIP `10.96.137.111`, 5432).
- Crossplane provider-sql gives each `ServiceDatabaseV2` its own **database** +
  role `svc-<name>`; the real endpoint/user/pass live in the connection secret
  `<name>-db-credentials` (namespace = the app namespace) keys
  `endpoint/port/username/password`.
- No external exposure today (`postgresql` = ClusterIP).
- nginx-pub LB already exists: `155.212.223.198` (ns `network`), and a TCP
  passthrough ConfigMap `ingress-nginx-network-tcp-services` is in use.

## Why not the naive approaches

- **Raw TCP passthrough of `postgresql:5432`** → one public port hitting the
  shared instance. Anyone who reaches it can attempt auth as ANY role/db.
  Network isolation = none. Rejected for a multi-tenant shared instance.
- **Port-per-db passthrough** (one public port per opt-in db → same shared
  backend) → still lands on the shared instance; a client on db A's port can
  auth as db B's role. Cosmetic isolation only. Rejected.

## Chosen architecture: pooler in front, allow-list gated

```
external client
      │  TLS, :5432
      ▼
nginx-pub LB 155.212.223.198  (TCP passthrough, tcp-services 5432 → pooler svc)
      ▼
pgcat / pgbouncer  (ns databases)   ← allow-list: only opted-in <db>+<role>
      ▼
postgresql.databases.svc.cluster.local:5432  (shared instance, unchanged)
```

- The pooler config lists ONLY databases whose `ServiceDatabaseV2` has
  `spec.external.enabled: true`, each pinned to its `svc-<name>` role. A
  connection for any non-listed db/role is refused at the pooler -- true per-db
  gating over a single public port.
- Public host = a dedicated DNS name (e.g. `db.dada-tuda.ru`) → `155.212.223.198`.
- `sslmode=require` enforced at the pooler (Postgres wire TLS, not HTTP).

## Contract between repos

### 1. Console (this repo, dada-cloud) -- DONE (additive, dormant until infra)

- Create flow: optional `external_enabled` (bool) on
  `POST .../databases` → `CreateServiceDatabasePayload.ExternalEnabled` →
  rendered into the CR as `spec.external.enabled`.
- Reveal (`GET .../databases/{name}/credentials?reveal=true`): if the connection
  secret carries an external endpoint, return it. Secret keys read (first
  non-empty wins): `external_endpoint` (host:port) OR `external_host` +
  `external_port`. Absent → response omits external fields (internal-only, as
  today).
- Frontend: create-modal toggle "Публичный доступ" + reveal shows the external
  host with a security note; internal host still shown.

### 2. ServiceDatabaseV2 composition (external Crossplane repo) -- TODO

- Honor `spec.external.enabled`. When true:
  - Add this db+role to the pooler allow-list (see infra).
  - Publish `external_endpoint` (or `external_host`/`external_port`) into the
    `<name>-db-credentials` connection secret so the console can reveal it.

### 3. argo-infra (mgmt cluster / gitops) -- TODO

- Deploy pgcat (or pgbouncer) in ns `databases`, config generated from the set
  of opt-in databases (allow-list of db+role, upstream = shared postgres).
- Add `5432: databases/<pooler-svc>:5432` to
  `ingress-nginx-network-tcp-services` and expose 5432 on the pub LB.
- DNS `db.dada-tuda.ru` → `155.212.223.198`.
- TLS material for the pooler (Postgres wire TLS).

## Security notes

- Only explicitly opted-in databases are reachable; default stays private.
- Isolation is enforced at the pooler allow-list, not just by password.
- Every reveal is already write-gated + audited (`RevealDatabaseCredentials`).
- Recommend per-db source-IP allow-list (optional `spec.external.allowedCidrs`)
  as a follow-up; pgcat/pgbouncer + nginx can enforce.

## Status / blockers

- Console: shipped, safe, no-op until the secret carries external keys and the
  CR field is honored.
- Blocker: composition change + pgcat deploy in argo-infra. Until then the
  toggle sets a CR field nothing consumes and reveal shows internal-only.
