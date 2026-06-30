# Telemetry retention — where it lives & how to change it

Goal: USER-pushed cloud telemetry (the product) retained **15 days by default**, as a
**gitops-configurable** value, kept **separate** from cluster-pod infra telemetry.

Topology investigated + split executed 2026-06-30. User and infra telemetry now live in
**separate stores** for both metrics and logs.

| Store | What it holds | Retention knob (gitops, argo-infra) | Default |
|-------|---------------|--------------------------------------|---------|
| **USER metrics** | OTLP gateway + external VM remote-write | `apps/user-telemetry-prometheus/chart/values.yaml` → `retention` | **15d** |
| INFRA metrics | cluster-pod scrape | `apps/kube-prometheus-stack/values.yaml` → `prometheus.prometheusSpec.retention` | 7d |
| **USER logs** | `dada-app-logs-*` | `apps/dada-app-logs-ilm/chart/values.yaml` → `retentionDays` | **15d** |
| INFRA logs | `filebeat-*` | `apps/elastic-stack/values.yaml` → `filebeat-prod-policy` | 7d/14d |

## Topology — two dedicated metric stores

- **USER metrics → `user-telemetry-prometheus`** (ns `monitoring`): a dedicated Prometheus,
  reconciled by the existing kube-prometheus-stack operator (it watches all namespaces).
  `enableRemoteWriteReceiver: true`, **scrapes nothing** (all selectors nil), 15d retention,
  own `longhorn-cache` PVC. Service: `user-telemetry-prometheus.monitoring.svc:9090`.
- **INFRA metrics → `monitoring-stack-prometheus`** (the kube-prometheus-stack instance):
  cluster-pod scrape only, 7d. Its remote-write receiver stays enabled but is now unused.

### Write path (how user metrics reach the user store)
- **OTLP gateway** (`backend/cmd/gateway`): `PROMETHEUS_REMOTE_WRITE_URL` is unset, so it
  falls back to `PROMETHEUS_QUERY_URL` for remote-write (`backend/cmd/gateway/main.go`).
  That URL is now the user store (see below) → gateway writes land in the user store.
- **External user VMs**: prometheus-agents push to `prometheus-dada-prod.dada-tuda.ru/api/v1/write`
  (basic-auth). Ingress `vm-metrics-write` (app `vm-observability`) backends the user store.

### Read path
- Console reads user metrics from `global.shared.prometheusQueryUrl`, set in the
  `cloud-console` app values to `http://user-telemetry-prometheus.monitoring.svc:9090`.
  Same key the gateway falls back to — one value moves read + gateway-write together.

## Topology — logs (per-index ILM)
- `dada-app-logs-*` (USER): ILM `dada-app-logs-policy` (delete @ 15d) + index template, applied
  by app `dada-app-logs-ilm` (bootstrap Job PUTs to Elasticsearch).
- `filebeat-*` (infra) `filebeat-prod-policy` 7d/14d and `dada-vm-logs-*` are untouched.

## How to change retention

| Want to change | Edit (argo-infra) | Field |
|----------------|-------------------|-------|
| User metric retention | `apps/user-telemetry-prometheus/chart/values.yaml` | `retention` (and `retentionSize`/`storage.size` cap) |
| User log retention | `apps/dada-app-logs-ilm/chart/values.yaml` | `retentionDays` |
| Infra metric retention | `apps/kube-prometheus-stack/values.yaml` | `prometheus.prometheusSpec.retention` |

Commit + push to branch `console-migration` → ArgoCD (automated sync) applies. Metric stores
roll the Prometheus pod; the log ILM re-runs its bootstrap Job (idempotent PUT).

## Storage note (longhorn)
- The user store provisions a **new** `longhorn-cache` PVC (8Gi, 1 replica) — same storage
  class the infra Prometheus (8Gi) and Elasticsearch (20Gi) already use successfully there.
  This is NEW-PVC provisioning, not the historically-blocked **expansion** of an existing PVC.
- If 15d of user series ever exceeds the disk cap (`retentionSize "6GiB"`), raise both
  `retentionSize` and `storage.size` together — but a fresh larger PVC, not an in-place resize.

## Limitations (honest)
- **No per-tenant retention.** The user store is a single shared Prometheus across all tenants;
  Prometheus has one global retention. Global-for-user-store 15d is delivered. True per-tenant
  retention would need a multi-tenant TSDB (VictoriaMetrics/Mimir) — not deployed.
