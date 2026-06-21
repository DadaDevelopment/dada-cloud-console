# PRD: DADA Cloud Monitoring

## Status

Draft — 2026-06-21

Related: [ADR-011: Monitoring Ingestion & Grafana-Native Alerting](../adr/ADR-011-monitoring-ingestion-grafana-native.md), [PRD-IAM](PRD-IAM.md)

## Summary

External monitoring for customer apps and devices. Users register a **Monitoring resource** inside a project (a new project resource kind, alongside the existing DB / VM / App resources), get an API key, and push metrics and logs over REST. The console shows an app card (health / last metrics / last logs) and dashboards; alerts fire to Telegram / Email / Webhook.

We do **not** write the monitoring stack. Prometheus, Grafana, and Elasticsearch are given. Our code is: ingestion API, IAM integration, health computation, and UI on top.

## Goals

- Register a Monitoring resource (project + environment + API key).
- `POST /metrics` ingestion (cpu, memory, temperature, custom).
- `POST /logs` ingestion (source, level, message).
- App card: health, last metrics, last logs.
- Dashboards: CPU / RAM / Temperature / custom metrics.
- Alerts: Telegram / Email / Webhook.

## Non-Goals (MVP)

- Building alert evaluation/routing ourselves (Grafana Alerting owns it).
- VictoriaMetrics / Loki (we reuse Prometheus + Elasticsearch).
- SLA/uptime reports, cost metrics.
- Migrating existing VM/infra logs off Elasticsearch.

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
