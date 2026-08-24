# Publishing an app-server VM on a platform hostname

Status: implemented (2026-08-25), not yet released.
Trigger: 2026-08-24, project `agents`, app server `harness-vps`
(`89e640c0-bdba-49e3-a58c-532f1792e230`, vm_ip `89.169.39.169`, Portainer endpoint 21).
Publishing `harness.dada-tuda.ru` needed a hand-committed `PublicApi` CR in
argo-infra (`DADA_MANUAL_OK=1` past the console-managed-file hook) plus a
hand-installed Caddy over SSH.

Goal, now met: after `createAppServer`, ONE call gives the VM a working
`https://<name>.dada-tuda.ru` — the platform owns the nginx, issues the cert,
writes the A record, and the workload keeps its services on loopback.

## What already existed

The claim "the platform can only discover a reverse proxy, never deploy one" was
true of the *adopt* path only. The *create* path already shipped nginx + ACME:

| Piece | Where |
| --- | --- |
| Ingress API | `POST /projects/{projectId}/environments/{envId}/ingress` → `CreateIngress` ([ingress.go](backend/internal/api/ingress.go)) |
| nginx on the VM | `ingressComposeBlock` ([dbwatcher.go](gitops-agent/internal/worker/dbwatcher.go)) — `nginx:1.27-alpine`, 80/443, conf shipped base64 in `NGINX_CONF_B64` |
| Certificates | `certbotComposeBlock` — webroot HTTP-01, per-host `--cert-name`, 30-min renew loop; deferred 443 blocks in `/etc/nginx/tls.d` so nginx boots before the cert exists |
| Extra vhosts on a VM | `doAttachCustomHostnameCompose` |
| A-record composite | `RenderDefaultDomainDNS` — `PublicApi` with `gatewayRoute: false`, `dns.target` a free parameter, composition `publicapi-beget-dns` |

Production proof: `agent-sandbox/le-probe` is a console-authored VM ingress whose
`compose.yaml` in argo-infra still carries the platform certbot block
(`--webroot -w /var/www/acme`, `--cert-name`,
`CERTBOT_HOSTS: le-probe.pv.dada-tuda.ru`) generated verbatim by
`certbotComposeBlock`. By contrast `fin-core/findata` nginx is an ADOPTED stack
(hand-written conf, `/var/www/certbot` webroot) — not platform-generated.

Four things were missing. All four are now closed.

## Blocker A — a VM had no environment until an import ran (CLOSED)

The `INSERT INTO environments ... ON CONFLICT` was buried inside
`ImportComposeStack`, and there is no `POST .../environments` route, so
`CreateIngress` and `AttachHostname` — both keyed by `envId` — were unreachable
for a fresh VM.

Shipped: `ensureVMEnvironment` ([appservers.go](backend/internal/api/appservers.go)),
called by both `ImportComposeStack` and the new hostname handler. Name/namespace
stay `<serverName>` / `<projectSlug>-<serverName>`, so a later import lands on
the same row.

## Blocker B — nginx could not proxy to a host loopback port (CLOSED)

Shipped: `HostLoopback` on `VMIngressRule` and `VMExtraHost`
([vm_resources.go](gitops-agent/internal/renderer/vm_resources.go)). When set,
`RenderNginxConf` emits `proxy_pass http://host.docker.internal:<port>;` and
`ingressComposeBlock` adds `extra_hosts: ["host.docker.internal:host-gateway"]`
via `VMIngressSpec.NeedsHostGateway()`. A loopback upstream is not a compose
service, so it deliberately produces no `depends_on` entry.

The per-VM map is the `ingress` meta object already persisted on the App snapshot
(`host, aliases, rules[], custom_hosts[]`, marked `managed: "ingress"`) — rules
and custom hosts gained a loopback flavour, nothing new to store. Certificates
come free: `certbotComposeBlock` already issues per host.

## Blocker C — a VM hostname could only be a CUSTOM domain (CLOSED)

`doAttachDefaultDomain` was the only path that writes a platform A/CNAME record
and it was k8s-only; the VM branch wrote no DNS at all, and `AttachHostname`
demands a verified apex authorization that no tenant can pass for
`dada-tuda.ru`.

Shipped:

- `POST /projects/{projectId}/app-servers/{serverName}/hostname` →
  `AttachAppServerHostname` (MCP `attachAppServerHostname`). No import first.
  Mints a managed `A` hostname under `DefaultDomainBase`, or validates a
  caller-supplied one; `target_port` + `host_loopback` publish a service bound to
  `127.0.0.1` on the VM. Rejects `server_not_enrolled` / `server_not_ready` /
  `server_has_no_ip`, is idempotent inside the same env, 409 `hostname_taken`
  across envs. One tx writes the `domain_hostnames` row and the
  `AttachDefaultDomain` operation; 202.
- VM branch in the agent: `doAttachDefaultDomainCompose` renders
  `RenderDefaultDomainDNS` with `Target = app_servers.vm_ip` and attaches the
  vhost to the managed ingress, creating it when the env has none
  (`ensureManagedIngress`, catch-all `server_name _`, no rules, no cert of its
  own; every published hostname is an `ExtraHost` with its own cert).
  `envWebPortHolder` refuses to add an ingress under an app that already
  publishes host :80/:443 — two containers on one host port fail the whole stack
  and would take down the app that was serving.

`CreateApp` on a VM env now also mints a default `A` hostname, gated on the
server having a `vm_ip`, and publishes it INLINE inside `doCreateComposeApp` —
one commit carries service + vhost + DNS. Two operations would race: nginx
resolves `proxy_pass` upstreams at startup, so a vhost committed before its
service crashloops the whole ingress.

### Reconcilers, decided per pass

- `ReattachOrphanedHostnames` — widened to both runtimes (re-issue target is the
  vm_ip for a VM hostname, `ClusterLBIP` for k8s). A loopback carrier hostname is
  never selected because it has no App snapshot.
- `ReapOrphanedAppHostnames` — stays k8s-only. A loopback carrier has no App
  snapshot, so widening it would tombstone live hostnames as `app_deleted`.
- `BackfillMissingDefaultDomains` — stays k8s-only on purpose. Adopted/imported
  VM containers would get auto-published; `appMayGetDefaultDomain` exists because
  implicit publishing bit us once.

Also fixed here: `ReconcilePendingHostnames` compared managed DNS against
`cfg.ClusterLBIP` for every runtime, so a VM hostname (target = vm_ip) never
resolved and re-issued on every cooldown. k8s behaviour is byte-identical.

### The compose snapshot has no top-level `port`

`composeAppSummary` writes only `runtime`/`status`/`desired`, so every domain
pass keyed on `summary["port"]` was a silent no-op on VM — it would have looked
widened while deciding nothing. `summaryServicePort` now reads the top-level
`port` first, then `desired.ports[]`; `containerPortFromPortString` takes the
LAST segment (container side). Locked by
[vm_default_domain_test.go](backend/internal/api/vm_default_domain_test.go),
proven RED against the old read.

## Blocker E — a published VM app had no address in the console (CLOSED)

Found while auditing parity, not in the original list. `syncStackSnapshots`
([stack_snapshots.go](portainer-agent/internal/worker/stack_snapshots.go)) wrote
`image/status/live_source/stack/endpoint_id` and nothing else, while the k8s
status reconciler has always patched `url`/`url_status`/`url_reason` from
`db.PrimaryHostname`. A VM app could therefore be published, certificated and
serving while the console showed it with no address at all.

Shipped: `db.PrimaryHostname` in portainer-agent (active beats pending, a
tenant's own domain beats the platform surrogate, ties oldest-first) plus
`applyPrimaryHostname` / `hostnamePatchFields` on the live-status patch. The keys
are always written — nil when there is no hostname — because the patch is merged
into `summary_json`, so omitting them would keep advertising a detached domain.

## Blocker D — MCP allowlist (CLOSED)

`keep:` in `backend/internal/mcp/default_overrides.yaml` gained
`discoverAppServerWorkload`, `importComposeStack`, `attachAppServerHostname`
(43 → 46 tools; the count gate in `advertised_surface_test.go` spans 8
frontend/marketing/plugin files plus the per-tool row in
`mcp-tool-reference.md`). `createIngress` stays out on purpose:
`attachAppServerHostname` creates the managed ingress itself, so exposing a
second, lower-level way to bind :80/:443 only invites the conflict
`envWebPortHolder` exists to prevent.

## Tests

- `backend/internal/api/vm_default_domain_test.go` — snapshot port reading and
  `appNeedsDefaultDomain` on compose snapshots.
- `gitops-agent/internal/worker/vm_managed_ingress_test.go` —
  `hostPortFromPortString` host-vs-container side; the catch-all renders no 443
  block without a cert; a loopback host renders `host.docker.internal` upstream
  plus the `host-gateway` mapping; ACME challenge location, deferred TLS include,
  `NGINX_TLS_BLOCKS` and both mounts appear the moment there is a host to issue
  for.

- `portainer-agent/internal/worker/stack_hostname_test.go` — a published VM app
  carries the https url and its status; a detached one clears the keys instead of
  omitting them; the failure reason rides along with the address.

Each test was shown RED against a mutated source before being accepted.

## Provisioning security — world-open metrics ports (NOT FIXED)

Not the user's workload. Source is the fleet edge stack, not the SSH bootstrap:
`argo-infra: clusters/beget-prod/fleet/vm-observability/docker-compose.yml`
(edge stack `vm-observability`, group `dada-vms`, delivered to the whole fleet).

| Service | Why it is public | Port |
| --- | --- | --- |
| `node-exporter` | `network_mode: host` + `--web.listen-address=:19100` | 19100, HTTP 200, no auth |
| `prometheus-agent` | `network_mode: host`, no `--web.listen-address`, so Prometheus defaults to `0.0.0.0:9090` | 9090, 302, no auth |
| `cadvisor` | `ports: ["18080:8080"]` binds all interfaces | 18080, no auth |

All three are scraped only from `localhost` by the agent on the same host net,
and metrics leave by `remote_write` (push). Binding them to loopback costs
nothing:

- node-exporter: `--web.listen-address=127.0.0.1:19100`
- prometheus-agent: add `--web.listen-address=127.0.0.1:9090`
- cadvisor: `ports: ["127.0.0.1:18080:8080"]`

One file edit + edge-stack redeploy fixes every VM in the fleet. Not applied:
argo-infra is prod and this is a separate decision from the hostname work.
