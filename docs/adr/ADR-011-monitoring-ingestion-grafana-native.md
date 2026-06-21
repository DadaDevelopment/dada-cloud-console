# ADR-011: Monitoring — Prometheus Remote-Write Ingestion + Grafana-Native Alerting/Dashboards

## Status

Proposed — 2026-06-21

Related: [PRD-monitoring](../prd/PRD-monitoring.md)

## Context

DADA Cloud Monitoring needs to let customer apps/devices push metrics and logs over REST, then show health, dashboards, and alerts. The stated principle: **don't write the monitoring stack** — Prometheus, Grafana, Elasticsearch are given; our code is ingestion + IAM + UI.

What exists today is **read-only**:
- `prometheus/client.go` — PromQL `query_range` against a central Prometheus (assumes remote_write from portainer-agent).
- `logsearch/client.go` — Elasticsearch search over `dada-vm-logs-*`.
- `metrics-panel.tsx` / `metric-chart.tsx` / `logs-viewer.tsx` — native SVG charts + log viewer.

Missing entirely: any **write path**, any alerting, any dashboard provisioning. Prometheus is pull-only — wrong for app-pushed metrics.

## Decision

### 1. Metrics: Prometheus with remote-write receiver

Enable `--web.enable-remote-write-receiver` on the existing Prometheus. The ingest handler converts incoming JSON metrics → Prometheus remote-write protobuf/snappy → `POST /api/v1/write`. Labels: `{__name__, org_id, project_id, environment, source, monitoring_app}`. The query path is unchanged — existing `prometheus/client.go` reads it back. No VictoriaMetrics, no Pushgateway.

### 2. Logs: reuse Elasticsearch

App logs ingest into Elasticsearch (`dada-app-logs-*`). Reuse the existing `logsearch/client.go` + `LogsViewer`. No Loki — one log store, one read path, fastest ship. (PRD mentioned Loki; reuse of the proven ES read path was chosen over a second store.)

### 3. Monitoring resource = new project resource kind

A Monitoring resource lives inside a project alongside DB / VM / App resources. Table `monitoring_apps` (`id, project_id, environment_id, name, created_at`). It is an ingest target (may be an external device), with its own scoped API key (issued via user-service IAM). Not coupled to deployed workloads.

### 4. Ingestion endpoints, scope-gated

```
POST /api/v1/projects/{projectId}/monitoring/{appId}/metrics   scope: metrics:write
POST /api/v1/projects/{projectId}/monitoring/{appId}/logs      scope: logs:write
```

Auth via API-key→gateway exchange→fat claims; handler enforces scope + org/project isolation from claims, then forwards to Prometheus remote-write / Elasticsearch.

### 5. Alerting: Grafana Alerting (not our own engine)

dada-cloud CRUDs Grafana alert rules + contact points via the Grafana HTTP API, tagged by org/project. Channels = native Grafana contact points: **Telegram, Email, Webhook**. Grafana owns evaluation, dedup, routing, silencing. Email shares SMTP with IAM invitations. Rules mirrored lightly in Postgres for the native list UI.

### 6. Dashboards: hybrid (native card + Grafana deep-link)

Console renders the resource card + sparklines natively (reuse `MetricsPanel`/`MetricChart`, JWT-authed). The rich dashboard is a provisioned Grafana dashboard (one per resource), deep-linked/embedded. Same Grafana API client provisions both alerts and dashboards. Custom metrics auto-add native panels by metric name.

### 7. Health: liveness + error-rate + active alerts

States `healthy / degraded / down / unknown`, plus `critical` when a Grafana alert is firing. Computed in dada-cloud from: last-seen timestamp (`down` if no data > 5 min, `unknown` if never), ES ERROR-rate over 15 min (`degraded`), and a Grafana firing-alerts pull (`critical`). Thresholds configurable per resource.

## Alternatives considered

| Decision | Options | Chosen | Why |
|----------|---------|--------|-----|
| Metrics store | Prometheus remote-write / VictoriaMetrics / Pushgateway | **Prometheus remote-write** | Owner's call; reuses existing Prometheus + `prometheus/client.go`, push-native, no new store |
| Logs store | Loki / reuse Elasticsearch | **Elasticsearch** | Owner's call; reuse proven read path + viewer, fastest ship |
| Monitoring entity | new project resource kind / reuse deployed apps / labels-only | **project resource kind** | Owner's framing: a resource like DB/VM/App; supports external devices + real registration |
| Alerting | Grafana Alerting / Alertmanager / own engine | **Grafana Alerting** | "Don't write the stack"; all 3 channels native; owns eval/routing/dedup |
| Dashboards | native / embed Grafana / hybrid | **hybrid** | Native card avoids iframe-auth hell; Grafana for rich view |
| Health | liveness / +error-rate / +alerts | **+alerts (full)** | Richest signal; ties card to alerting |

## Consequences

**Positive**
- Minimal new code: ingest handlers + a Grafana API client + health computation. Read path, charts, and log viewer reused as-is.
- No new datastore; Prometheus + ES already operated.
- Alert evaluation/routing fully delegated to Grafana.
- Label-driven series → custom metrics need no code changes.

**Negative / risks**
- Running app-pushed metrics through Prometheus remote-write needs cardinality discipline (per-device `source` labels can explode series) — enforce label limits + per-key rate limiting at ingest.
- Two log indices in ES (infra `dada-vm-logs-*`, app `dada-app-logs-*`); PRD's Loki preference deferred.
- Alert rules live in Grafana (mirrored copy in Postgres can drift) — Grafana is source of truth.
- Grafana multi-tenant isolation (org/project tagging + folder/permission model) must be configured so customers can't see each other's dashboards/alerts.

## Validation plan

1. Device POSTs metrics with a `metrics:write` key → series visible via existing query path within seconds.
2. Device POSTs logs with a `logs:write` key → visible in `LogsViewer`.
3. A CPU threshold rule fires to Telegram + Email + Webhook.
4. Health flips `down` on silence, `critical` on firing alert.
5. Cross-org isolation: org A cannot query org B's series/logs/dashboards.
