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

## Parameter mapping — findata REAL config → Resource spec (proof)

Read live from findata (read-only via the docker proxy). Every nginx/postgres
parameter maps to a field on a runtime-agnostic Resource spec — the SAME spec the
k8s provider already consumes. This is the concrete evidence that "we can express
the exact same params via Ingress / ServiceDatabase and describe them the same way
for VM".

### nginx (real) → Network/Ingress Resource

Live directives: `server_name fin-data.pro` (+ `www.fin-data.pro`),
`ssl_protocols TLSv1.2 TLSv1.3`, `auth_basic "Private area"`,
`location /api/ → proxy_pass http://backend:8001`,
`location / → proxy_pass http://frontend:5173`, `ssl_certificate .../live/fin-data.pro/*`.

```yaml
Ingress:                         # nginx directive it renders from:
  host: fin-data.pro             #   server_name ${DOMAIN}
  aliases: [www.fin-data.pro]    #   server_name www.* → 301 redirect to host
  sslRedirect: true              #   listen 80 → return 301 https
  tls:
    enabled: true                #   listen 443 ssl http2
    minVersion: "1.2"            #   ssl_protocols TLSv1.2 TLSv1.3
    cipherProfile: modern        #   ssl_ciphers HIGH:!aNULL:!MD5
    cert: letsencrypt(fin-data.pro)  #   ssl_certificate .../live/fin-data.pro/{fullchain,privkey}
  auth:
    basic: { credentialsRef: <htpasswd/secret> }  # auth_basic + auth_basic_user_file
  rules:
    - path: /api/  → { app: backend,  port: 8001 }   # location /api/ proxy_pass backend:8001
    - path: /      → { app: frontend, port: 5173 }   # location /      proxy_pass frontend:5173
  # X-Forwarded-* / Host headers = default proxy behavior, not user params
```
- **k8s provider** → `Ingress` CR + nginx-ingress annotations (auth-basic, ssl-redirect) + cert-manager cert.
- **VM provider** → the nginx service + **generates** `conf.d` from `rules`/`tls`/`auth` (no hand-edited template; upstream = the Application service names).

### postgres (real) → ServiceDatabase Resource

Live: `server_version 16.13`, `max_connections 100`, `shared_buffers 128MB`
(image defaults), db `feedback`, user `postgres`, volume `compose_profi_pg_data`
(external), host port `65433:5432`.

```yaml
ServiceDatabase:
  engine: postgres
  version: "16"                  # 16.13
  database: feedback             # POSTGRES_DB
  storage: { adopt: compose_profi_pg_data }   # external volume — data-safety invariant
  params:                        # postgresql.conf tunables (defaults now, overridable)
    max_connections: 100
    shared_buffers: 128MB
  exposure: { hostPort: 65433 }  # optional; internal (service DNS) by default
  backup: { enabled: false }     # toggle → scheduled pg_dump (VM) / CNPG backup (k8s)
  # platform owns user + password (rotate-able) → DATABASE_URL injected into consumers
```
- **k8s provider** → `ServiceDatabaseV2` CRD (CNPG), `params` → postgres cluster config.
- **VM provider** → postgres compose service + external volume + `params` → a mounted
  `postgresql.conf` (or `-c` flags) + managed creds + backup sidecar.

**Takeaway:** the nginx.conf and postgres settings contain nothing that isn't a
field on the Ingress / ServiceDatabase spec. So the user describes an Ingress or a
Database ONCE (same form for k8s and VM); the runtime provider renders it. The VM
stops carrying hand-written nginx templates / raw postgres env — those become
generated output of the Resource spec.

## Boundary (this session)

Design only. findata prod stays on the current single managed `profi-vm` stack
(actualized, healthy). Implementation belongs to the VM-app-model architecture
track; this note is its input. Could be promoted to an ADR (e.g. ADR-014
"Runtime-agnostic Resource Providers").
