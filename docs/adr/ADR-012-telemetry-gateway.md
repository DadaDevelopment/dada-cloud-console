# ADR-012: Telemetry Gateway — separate ingest service, OTLP-native, label-injecting on Prometheus + ES

## Status

Proposed — 2026-06-25

Related: [ADR-011: Monitoring Ingestion & Grafana-Native](ADR-011-monitoring-ingestion-grafana-native.md), [PRD-monitoring](../prd/PRD-monitoring.md), [telemetry-gateway-design](../../tasks/telemetry-gateway-design.md)

## Context

ADR-011 shipped monitoring end-to-end inside the console backend monolith (commit `6552d15`): bespoke-JSON ingest → Prometheus remote-write + Elasticsearch, `dmon_` keys, health, Grafana provisioning, alerts, UI.

The product is being repositioned (see PRD-monitoring "Product vision"): monitoring is a **push/pull telemetry gateway** — any external workload (app, server, IoT device) pushes telemetry with a scoped API key and gets dashboards/health/alerts. The near-term client runs Node.js on IoT devices and will emit via the **stock OpenTelemetry SDK (OTLP)**.

Two gaps versus that vision:
1. Ingest only speaks bespoke DADA JSON — no standard protocol. OTLP is required.
2. Ingest lives in the console request path — wrong blast radius for high-volume, untrusted, spiky device traffic.

## Decision

### 1. Ingest moves to a separate stateless **Telemetry Gateway** service

A new Go service, deployed and scaled independently from the console backend. It owns the **write plane** (all device-facing ingest). The console keeps the **control plane** (resource/key CRUD, RBAC) and **read plane** (health, metrics/logs read-back, alerts, dashboards).

Why separate:
- Ingest is high-volume, spiky (IoT fleets), and authenticated by device keys, not user JWTs. Isolating it keeps console latency and rollouts independent of device traffic.
- Horizontal scale on the ingest hot path without scaling the console.
- Smaller, hardened attack surface for the public ingest endpoint.

Both services share the same Postgres (`monitoring_apps`) and the same Prometheus + ES. The gateway reads `monitoring_apps` directly (read-only DB role) to verify keys — this is the local stand-in for the PRD's "gateway exchange → fat claims" seam.

### 2. OTLP/HTTP is first-class, via the official proto

Endpoints follow the OTLP/HTTP spec so a stock OTel exporter works with only endpoint + key:

```
POST /v1/metrics    application/x-protobuf | application/json    scope: metrics:write
POST /v1/logs       application/x-protobuf | application/json    scope: logs:write
```

Decoding uses `go.opentelemetry.io/proto/otlp` (official). We do **not** hand-roll OTLP (unlike the outbound remote-write protobuf in ADR-011 / monitoring-write-path.md — that is our own small stable format; OTLP is a large inbound third-party schema where hand-rolling invites bugs).

Bespoke JSON endpoints are retained for back-compat and move to the gateway too.

### 3. `appId` is resolved from the key, not the path (native OTLP UX)

OTel exporters point at a fixed `OTEL_EXPORTER_OTLP_ENDPOINT` and cannot put our `appId` in the path. The gateway resolves the monitoring resource **from the `dmon_` key** (prefix-indexed lookup → argon2id verify). Device config stays clean: endpoint + key header, nothing else.

### 4. Gateway is **thick**: decode + inject authoritative tenant labels (path A)

Because we stay on Prometheus + Elasticsearch (decided below), the gateway must enforce tenancy itself:

- Decode OTLP → flatten metric data points / log records.
- Inject authoritative `org_id` / `project_id` / `environment` / `monitoring_app` from the resolved DB row. The OTLP payload is **never trusted** for tenancy; client `service.name` maps to the `source` label only.
- Forward: metrics → existing Prometheus remote-write (`prometheus/remotewrite.go`); logs → existing ES writer (`logsearch/write.go`).
- Reuse the per-app rate limiter + per-request metric-count cardinality cap.

Histograms are decoded on v1: OTLP explicit-bucket histogram → Prometheus `_bucket{le}` / `_sum` / `_count` series. Exponential histograms deferred (convert-or-drop, logged).

### 5. Store stays Prometheus + Elasticsearch now; Mimir + Loki is the documented evolution

We keep the shipped stack and read path. Plain Prometheus is **not** multi-tenant, so isolation rests entirely on our injected labels and query-time label filters — accepted risk at current tenant count.

When tenant count/cardinality grows, migrate to **Grafana Mimir (metrics) + Loki (logs)**, which are natively multi-tenant via `X-Scope-OrgID`. At that point the **gateway gets thinner**: it stops decoding and instead proxies OTLP bytes straight through with the tenant header, letting Mimir/Loki parse and isolate. The gateway service boundary and device contract do not change across this migration — only its payload handling.

### 6. Dashboards: embedded Grafana (replacing self-written rich charts)

Rich dashboards are **embedded Grafana** (per-project folder + per-app dashboard, already provisioned by `grafana/client.go`), not hand-written SVG. The native SVG card is kept only for the at-a-glance health sparkline. Embedding auth (auth-proxy header / per-org service account / signed embed) is detailed in the design doc; this reverses ADR-011's "native card avoids iframe-auth hell" stance in favor of not maintaining a charting layer.

### 7. Onboarding is a guided, Langfuse-style flow (no white page)

The console must walk the user from zero to first telemetry: create resource → create API key (shown once) → copy a prefilled OTel code snippet (tabbed by language, Node.js first) → a live "Waiting for first telemetry" badge that flips to "Receiving" on first data point. This is a product requirement, not polish — it is the activation path for the gateway.

## Alternatives considered

| Decision | Options | Chosen | Why |
|---|---|---|---|
| Ingest placement | in console monolith / separate gateway service | **separate gateway** | blast radius, independent scale, device-key auth ≠ user JWT |
| OTLP decode | hand-roll proto / official `proto/otlp` | **official proto** | large inbound third-party schema; hand-roll = bugs |
| `appId` source | path param / from key | **from key** | stock OTel exporter can't put appId in path |
| Store | Prometheus+ES now / Mimir+Loki now / Mimir-metrics+ES-logs | **Prometheus+ES now** | reuse shipped code+read path, fastest for the client; Mimir/Loki later |
| Gateway weight | thick (decode+inject) / thin (proxy+header) | **thick** (path A) | plain Prom/ES aren't multi-tenant; thin needs Mimir/Loki |
| Dashboards | native SVG / embedded Grafana / hybrid | **embedded Grafana** (+SVG health glance) | stop maintaining a charting layer; Grafana is provisioned already |
| Give Prom/Grafana directly to clients | yes / no | **no** | no auth, no tenant isolation — gateway is mandatory |

## Consequences

**Positive**
- Stock OTel SDKs onboard with endpoint + key only — the broad-market gateway story.
- Ingest hot path isolated and independently scalable.
- Reuses the entire shipped read/health/alert/provisioning stack; no new datastore now.
- Migration path to true multi-tenant (Mimir/Loki) is documented and does not change the device contract.

**Negative / risks**
- Tenant isolation rests on our label injection + query filters until Mimir/Loki — one bug can leak across tenants. Mitigate: authoritative labels from DB only, query-time enforced filters, tests.
- A second deployable (gateway) to operate, plus its read-only DB access to `monitoring_apps`.
- Embedded Grafana brings the iframe-auth work ADR-011 dodged.
- Key-by-prefix lookup needs an index + collision-safe verify (prefix narrows, argon2id decides).

## Validation plan

1. Node.js device with stock OTel SDK (OTLP/HTTP), endpoint + `dmon_` key → metrics + logs land via existing read path within seconds.
2. OTLP protobuf **and** JSON both ingest.
3. Cross-tenant: org A's key cannot write or read org B's series/logs (label injection authoritative; query filters enforced).
4. Histogram metric renders `_bucket/_sum/_count` in an embedded Grafana panel.
5. Onboarding: fresh resource → guided steps → "Waiting for first telemetry" flips to "Receiving" on first data point.
6. Rate limit + cardinality cap reject abusive payloads with 429 / 413.
