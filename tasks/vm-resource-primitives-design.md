# VM infra as first-class cloud resources (ServiceDatabase + Network/Ingress)

**Design note (not to build here — feeds the VM-app-model architecture track).**
Owner idea: on the VM/compose track, **postgres and nginx must NOT be plain
"docker apps"** — they should be the **same first-class cloud Resources** the k8s
track already has, just rendered by a compose Runtime Provider.

## Principle (ADR-013 §5 Runtime Independence, applied to Resources)

A platform **Resource** is runtime-agnostic. Each runtime is a **Provider** that
renders the Resource into that runtime's shape:

```
Resource (spec, platform semantics)         Providers
  ServiceDatabase  ────────────────────►  k8s: CNPG/operator CRD (existing)
                                           VM : a postgres service block in the
                                                AppServer's compose + external
                                                volume + managed DSN/creds/backup
  Network/Ingress ─────────────────────►  k8s: Ingress CR + cert-manager (existing)
                                           VM : an nginx service + generated config
                                                (upstreams from the AppServer's
                                                Applications) + TLS
```

This is already true for **apps** (k8s Deployment vs VM compose service). Extend
the same shape to **infra resources**. Result: not everything on a VM is an
`Application` — some services are typed Resources (`ServiceDatabase`, `Ingress`),
exactly like k8s. (This refines the earlier "4 normal Applications" framing:
`api + frontend` = Applications; `postgres` = ServiceDatabase; `nginx` = Ingress.)

## Resource 1 — ServiceDatabase (postgres), VM provider

k8s today (`ServiceDatabaseV2`): generates creds, seeds `DATABASE_URL` DSN into
`env_vars`, manages backup (enabled/schedule/retention). Kind `ServiceDatabaseV2`,
its own "Databases" console view.

VM provider (new) must give the SAME contract, different rendering:
- Renders a `postgres` service into the AppServer's combined compose.
- **External volume ALWAYS** (the adopt/data-safety invariant — postgres data
  never lands in an anonymous volume; on adopt, pin the existing live volume).
- Managed **creds + DSN**: platform owns the credentials, injects
  `DATABASE_URL=postgres://…@<service>:5432/<db>` into the consuming Applications'
  env (same `seedEnvVar` path k8s uses; on VM the host is the compose service name).
- **Backup** on VM = a scheduled `pg_dump` (a small cron/sidecar service or a
  platform job container) writing off-box, honoring schedule/retention — same UX
  as the k8s backup toggle.
- Same console **Databases** view + API (`ListDatabases`/`CreateServiceDatabase`)
  — the `runtime` field decides the provider; the user sees a database, not a
  container.

Adopt case (findata): the running `postgres` service → a ServiceDatabase that
**adopts** `compose_profi_pg_data` + captures the existing init-time creds
(feedback db, user postgres) — no re-init, no data loss.

## Resource 2 — Network / Ingress (nginx), VM provider

NOT `PublicApi` — that is app-level public-API exposure (auth + swagger + Beget
DNS), a higher-level concern that *sits on top of* a route. This resource is the
**routing + TLS** primitive (the nginx role): which Application is served on which
host/path, with TLS.

k8s today: `Ingress` CR (+ `CustomIngress` with cert-manager letsencrypt HTTP-01
per-host TLS). VM provider (new):
- Renders the `nginx` service + **generates its config from the platform routing
  spec** — upstreams resolved to the AppServer's Application service names
  (`backend:8001`, `frontend:5173`), server_name = the host, TLS cert paths.
  The hand-edited `default.conf.template` disappears; routing becomes declarative.
- **TLS**: reuse the host letsencrypt certs (adopt case) or a managed cert
  resource; unify with the k8s cert-manager story over time.
- Console view = a "Routing"/"Ingress" resource per host → app, not an nginx
  container. PublicApi can then be layered on a route (auth/swagger) the same way
  it layers on a k8s Service.

Adopt case (findata): the running `nginx` → an Ingress/Route resource that adopts
the existing host binds (`/home/ubuntuuser/compose/nginx/*`, `/etc/letsencrypt`)
and the upstreams (`backend`, `frontend`) until the generated config replaces the
template.

## How the AppServer renders it all

The AppServer's ONE combined `compose.yaml` (from the app-model plan) is the sum of
its resources' provider blocks:

```
AppServer compose  =  Σ Application service blocks (api, frontend)
                    +  Σ ServiceDatabase provider blocks (postgres + external vol)
                    +  Σ Ingress provider blocks (nginx + generated config)
```

Each Resource contributes its service(s) + volumes; the AppServer aggregates and
deploys ONE stack. The user manages Applications, Databases, and Routing as
separate typed resources — compose is never surfaced.

## Import / discovery implication

Discovery classifies each service into the right Resource type (the earlier
app-vs-infra classifier was the right instinct — but "infra" should be *typed*
Resources, not a generic `Infra` kind):
- app image → `Application`
- postgres/mysql/mariadb/mongo/redis → `ServiceDatabase` (subtype)
- nginx/traefik/caddy/haproxy → `Network/Ingress`
The import wizard creates the correct Resource per included service.

## Payoff

- A VM postgres gets managed DSN, backup, creds rotation, the Databases view —
  parity with k8s DBs, not a raw container.
- A VM nginx gets declarative routing + unified TLS — not a hand-edited conf.
- One Resource model spans k8s + VM (+ future Swarm/Nomad) via providers, per
  ADR-013 §5. New runtimes reuse the same Resource UI/API.

## Boundary (this session)

Design only. findata prod stays on the current single managed `profi-vm` stack
(actualized, healthy). Implementation belongs to the VM-app-model architecture
track; this note is its input. Could be promoted to an ADR (e.g. ADR-014
"Runtime-agnostic Resource Providers").
