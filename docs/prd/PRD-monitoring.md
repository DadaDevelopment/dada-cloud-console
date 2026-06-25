# PRD: DADA Cloud Monitoring

## Status

Draft — 2026-06-21 · Product vision update — 2026-06-25

Related: [ADR-011: Monitoring Ingestion & Grafana-Native Alerting](../adr/ADR-011-monitoring-ingestion-grafana-native.md), [PRD-IAM](PRD-IAM.md)

## Product vision

Monitoring is **not** only about Apps deployed in our k8s/docker. As a product it is a **push/pull telemetry gateway**: any external workload — an app, a server, an IoT device — points at a single endpoint, throws telemetry at it with a scoped API key, and gets dashboards, health, and alerts back. No agent install, no infra to run on the customer side: generate a key with `metrics:write` / `logs:write`, push, get the magic.

The shape we sell:

> Generate API key → devices push metrics + logs in a **standard format** → customer gets a dashboard.

To make this real for the broad market the gateway must speak the **standard telemetry protocols**, not only our bespoke JSON. The de-facto standard for app/device telemetry is **OpenTelemetry (OTLP)** — it is what the OpenTelemetry SDKs (Node.js, Go, Python, …) emit out of the box. Supporting OTLP means a customer wires the stock OTel SDK at their `OTEL_EXPORTER_OTLP_ENDPOINT` and is done.

### Anchor scenario — IoT fleet (near-term client)

Customer runs Node.js apps on IoT devices. Devices heartbeat telemetry: metrics (cpu, memory, temperature, custom) and logs. Customer flow:

1. Create a Monitoring resource in their project → gets a `dmon_` API key (scopes `metrics:write`, `logs:write`).
2. Devices use the standard OpenTelemetry Node.js SDK, OTLP/HTTP exporter, endpoint = our ingest URL, key in the header.
3. Customer opens the console → health per device, metric dashboards, logs, threshold alerts to Telegram/Email/Webhook.

This is the validation target for the OTLP work below.

## Summary

External monitoring for customer apps and devices. Users register a **Monitoring resource** inside a project (a new project resource kind, alongside the existing DB / VM / App resources), get an API key, and push metrics and logs over REST — either our bespoke JSON or **OTLP/HTTP** (OpenTelemetry standard). The console shows an app card (health / last metrics / last logs) and dashboards; alerts fire to Telegram / Email / Webhook.

We do **not** write the monitoring stack. Prometheus, Grafana, and Elasticsearch are given. Our code is: ingestion API, IAM integration, health computation, and UI on top.

## Build status (2026-06-25)

End-to-end monitoring is **shipped** (commit `6552d15`): write path (JSON metrics → Prometheus remote-write, logs → Elasticsearch), `dmon_` scoped API keys (argon2id, plaintext-once), scope gate, per-app rate limit + cardinality guard, health computation, Grafana provisioning (folders/dashboards/alerts/contact points) + reconciler, and console UI (`monitoring/`, `monitoring/[appId]/`).

The one gap versus the product vision: ingestion only speaks **bespoke DADA JSON**. No standard protocol. **OTLP/HTTP ingestion is the net-new work** to make the gateway sellable to the broad market and to onboard the IoT client with a stock OTel SDK. Scope decided: OTLP **metrics + logs** only (no traces — no trace store; out of scope).

## Goals

- Register a Monitoring resource (project + environment + API key).
- `POST /metrics` ingestion (cpu, memory, temperature, custom) — bespoke JSON.
- `POST /logs` ingestion (source, level, message) — bespoke JSON.
- **OTLP/HTTP ingestion** (`/v1/metrics`, `/v1/logs`, protobuf + JSON) so stock OpenTelemetry SDKs push without our client — the product-vision deliverable.
- App card: health, last metrics, last logs.
- Dashboards: CPU / RAM / Temperature / custom metrics.
- Alerts: Telegram / Email / Webhook.

## Non-Goals (MVP)

- Building alert evaluation/routing ourselves (Grafana Alerting owns it).
- VictoriaMetrics / Loki (we reuse Prometheus + Elasticsearch).
- SLA/uptime reports, cost metrics.
- Migrating existing VM/infra logs off Elasticsearch.
- **OTLP traces / distributed tracing** (no trace store; separate ADR if needed).
- StatsD / InfluxDB line protocol (revisit per client demand; OTLP covers the standard case).

## What already exists (reuse)

- **Read path**: `prometheus/client.go` (PromQL `query_range`), `logsearch/client.go` (Elasticsearch), `metrics-panel.tsx` / `metric-chart.tsx` (native SVG charts), `logs-viewer.tsx`.
- Go/Gin REST pattern, nullable clients, JWT/claims auth.

## What is net-new

- Metric/log **ingestion** endpoints (write path).
- `monitoring_apps` project resource kind.
- Health computation.
- Grafana API client (alerts + dashboard provisioning).
- Alert configuration UI.

## Architecture

### Storage (decided)

- **Metrics → existing Prometheus with `--web.enable-remote-write-receiver`.** The ingest handler converts incoming JSON metrics → Prometheus remote-write protobuf/snappy → `POST /api/v1/write`. Series labels: `{__name__, org_id, project_id, environment, source, monitoring_app}`. Query path unchanged — existing `prometheus/client.go` reads it back.
- **Logs → existing Elasticsearch** (`dada-app-logs-*` index). Ingest handler writes app logs to ES; reuse existing `logsearch/client.go` + `LogsViewer`. One log store, one read path.

### Monitoring resource (project resource kind)

A Monitoring resource is a new kind inside a project, modeled like the existing DB / VM / App resources. It is an **ingest target** (may represent an external device/service, not a deployed workload).

- Table `monitoring_apps`: `id, project_id, environment_id, name, created_at`.
- Each carries a scoped API key (issued via user-service IAM, scopes `metrics:write,logs:write`).
- Card lists per resource: health + last metrics + last logs (filtered by labels).

### Ingestion API

```
POST /api/v1/projects/{projectId}/monitoring/{appId}/metrics    scope: metrics:write
POST /api/v1/projects/{projectId}/monitoring/{appId}/logs       scope: logs:write
```

Metrics body:
```json
{ "timestamp": "...", "source": "device-001",
  "metrics": { "cpu": 40, "memory": 512, "temperature": 60 } }
```

Logs body:
```json
{ "source": "device-001", "level": "ERROR", "message": "wifi disconnected" }
```

Auth: API key → gateway exchange → fat claims. Handler enforces scope from claim + org/project isolation, then forwards to Prometheus remote-write / Elasticsearch.

### OTLP ingestion (net-new — the product-vision work)

Add standard **OpenTelemetry OTLP/HTTP** endpoints next to the bespoke ones, behind the **same** `dmon_` key + scope gate. OTLP decoders fan the payload into the **existing** `promwrite` / `eswrite` clients — nothing downstream changes (storage, health, dashboards, alerts all reused).

```
POST /v1/metrics    Content-Type: application/x-protobuf | application/json   scope: metrics:write
POST /v1/logs       Content-Type: application/x-protobuf | application/json   scope: logs:write
```

- Paths follow the OTLP/HTTP spec (`/v1/metrics`, `/v1/logs`) so a stock OTel exporter works with only `OTEL_EXPORTER_OTLP_ENDPOINT` + the key header set. (We expose them under the project/app ingest group; the `appId` is resolved from the key, so the device config stays clean.)
- Both encodings: `application/x-protobuf` (SDK default) and `application/json`.
- **Decode → existing write clients:**
  - Metrics: OTLP `ResourceMetrics` → flatten gauge/sum/histogram data points → `prometheus.WriteSeries`. `__name__` = sanitized metric name. Resource/datapoint attributes become labels (cardinality-capped, same guard as today).
  - Logs: OTLP `ResourceLogs` → flatten `LogRecord` → `logsearch.AppLog`. `severityText`/`severityNumber` → `level`, `body` → `message`, attributes → fields.
- **Tenancy is authoritative from the key**, never from the OTLP payload: `org_id` / `project_id` / `environment` / `monitoring_app` injected from the resolved `monitoring_apps` row. Any client-supplied `service.name` maps to the `source` label only.
- Reuse the existing `ingestLimiter` (per-app token bucket) and the metric-count cardinality cap.

**Dependency note:** decoding OTLP means the OTel collector proto. Either pull `go.opentelemetry.io/proto/otlp` (clean, official, but a dep tree) or hand-roll the subset of the protobuf schema we read (consistent with the existing "no prometheus/prompb dep, hand-roll remote-write" decision in [monitoring-write-path.md](../../tasks/monitoring-write-path.md)). Decide at implementation; lean to the official proto for OTLP unless the tree is unacceptable, since OTLP message shapes are larger than remote-write.

**Traces: out of scope.** OTLP traces need a trace store (Tempo/Jaeger) we do not run. Not required for the heartbeat-telemetry scenario. Deferred to a separate ADR if a client needs distributed tracing.

### Health (computed in dada-cloud)

States: `healthy`, `degraded`, `down`, `unknown`. Inputs:

- **Liveness**: `down` if no metrics/logs in last 5 min (configurable). `unknown` if never reported.
- **Error rate**: `degraded` if ERROR-level logs over threshold in last 15 min (ES query, configurable per resource).
- **Active alerts**: `critical` if any Grafana alert is firing for this resource (pulled from Grafana API).

### Alerting (Grafana Alerting)

We do not build an alert engine. dada-cloud CRUDs Grafana alert rules + contact points via the Grafana HTTP API, tagged by `org_id` / `project_id`.

- Channels = native Grafana contact points: **Telegram, Email, Webhook**.
- Email contact point shares SMTP with IAM invitations.
- Rules live in Grafana; dada-cloud mirrors a lightweight copy in Postgres for the native list UI.

### Dashboards (hybrid)

- **Native card** in console: health badge + last metrics + sparklines (reuse `MetricsPanel` / `MetricChart`, authed via your JWT). Custom metrics auto-add a panel by metric name.
- **Grafana deep-link / embed** for the rich dashboard. Same Grafana API client that provisions alerts provisions one dashboard per Monitoring resource.

## UI

- Monitoring resource appears in the project resource list (next to DB/VM/App).
- Resource detail: health, last metrics (native sparklines), last logs (LogsViewer), "Open in Grafana".
- Alerts tab: create rule (metric + threshold + channel), list rules, channel config.
- API key issued on resource creation (plaintext once).

## Security

- Ingestion gated by `metrics:write` / `logs:write` scope on the key.
- Org/project isolation enforced from claims; series + log docs tagged with `org_id`/`project_id`, queries always filter by them.
- Per-key rate limiting on ingest (recommended).

## Success criteria

- A device POSTs metrics + logs with an API key and they appear in the resource card within seconds.
- A threshold alert on CPU fires to Telegram/Email/Webhook.
- Health correctly flips `down` when a device stops reporting and `critical` when an alert fires.
- Custom metrics render without code changes (label-driven panels).
