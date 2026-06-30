# Telemetry retention — where it lives & how to change it

Goal: USER-pushed cloud telemetry (the product) retained **15 days by default**, as a
**gitops-configurable** value, kept **separate** from cluster-pod infra telemetry.

This runbook records the real topology (investigated 2026-06-30, not assumed) and the exact
knobs. TL;DR:

| Store | What it holds | Retention knob (gitops) | Default | Separable from infra? |
|-------|---------------|--------------------------|---------|------------------------|
| Metrics (Prometheus) | USER OTLP + VM-agent remote-write **AND** infra-pod scrape | `prometheus.prometheusSpec.retention` | **15d** | **No** — single shared instance (see below) |
| Logs (Elasticsearch) | USER logs `dada-app-logs-*` | `dada-app-logs-ilm` chart `retentionDays` | **15d** | **Yes** — per-index ILM |

## Topology (the honest finding)

### Metrics — ONE shared Prometheus
- The OTLP gateway (`backend/cmd/gateway`) remote-writes user metrics to
  `PROMETHEUS_REMOTE_WRITE_URL`; when empty it falls back to `PROMETHEUS_QUERY_URL`
  (`backend/cmd/gateway/main.go`). In prod both resolve to
  `http://monitoring-stack-prometheus.monitoring.svc.cluster.local:9090`
  (`helm/dada-cloud-console/values.yaml` `prometheusQueryUrl`).
- `monitoring-stack-prometheus` **is** the `kube-prometheus-stack` instance
  (`fullnameOverride: monitoring-stack`). It is the **only** Prometheus with
  `enableRemoteWriteReceiver: true`. External user VMs also push to it via
  `prometheus-dada-prod.dada-tuda.ru/api/v1/write` (basic-auth ingress, app `vm-observability`).
- **Consequence:** user telemetry and infra-pod telemetry live in the **same TSDB**.
  Prometheus has a single global `--storage.tsdb.retention.time`. There is **no per-store
  or per-tenant retention**. Setting 15d necessarily applies to infra metrics too.
- **Why we did not silently raise it blind:** the side-effect is **bounded** by
  `retentionSize: "5GiB"` (hard disk cap, left unchanged). Effective retention is
  `min(15d, 5GiB-on-disk)`. The PVC stays `8Gi` on `longhorn-cache` — **no longhorn resize**
  (expansion has historically been blocked on this cluster).

### Logs — per-index ILM (clean separation)
- Infra K8s logs `filebeat-*` → ILM `filebeat-prod-policy` (7d hot / 14d delete), in
  `apps/elastic-stack/values.yaml`. **Untouched.**
- VM Docker logs `dada-vm-logs-*` → no ILM (bootstrap disables it). **Untouched.**
- USER product logs `dada-app-logs-*` → new ILM policy `dada-app-logs-policy` (delete at 15d),
  applied by the `dada-app-logs-ilm` app. This is genuine per-store separation — logs CAN be
  split by index, metrics cannot be split inside one Prometheus.

## How to change retention

### User metrics (currently 15d)
File: `argo-infra` →
`clusters/beget-prod/projects/platform/environments/prod/apps/kube-prometheus-stack/values.yaml`
- `prometheus.prometheusSpec.retention` — time retention (the knob). Set `15d`.
- `prometheus.prometheusSpec.retentionSize` — disk cap, `"5GiB"`. Leave as safety cap.
- Raising **both** retention AND retentionSize/PVC requires confirming longhorn headroom first
  (resize is risky here). Time-only changes are free.
- Commit + push to branch `console-migration` → ArgoCD (automated sync) rolls Prometheus.

### User logs (currently 15d)
File: `argo-infra` →
`clusters/beget-prod/projects/platform/environments/prod/apps/dada-app-logs-ilm/chart/values.yaml`
- `retentionDays: 15` — the knob. Change, commit, push. ArgoCD re-runs the bootstrap Job; it
  PUTs the ILM policy + index template into Elasticsearch (idempotent). Existing
  `dada-app-logs-*` indices adopt the delete phase from their own creation date.

## Limitations (state honestly)
- **No per-tenant retention** on the shared single-tenant Prometheus. Global-for-user-store 15d
  is the target and what is delivered.
- **No user/infra split for metrics** without new infrastructure. To truly separate, deploy a
  **dedicated** user-telemetry TSDB (a second Prometheus with its own remote-write receiver, or
  VictoriaMetrics for native multi-tenant retention) on persistent storage, then repoint the
  gateway `PROMETHEUS_REMOTE_WRITE_URL` + console `prometheusQueryUrl` at it. **Blocked on
  longhorn** (new persistent PVCs gated on this cluster); filed as a proposal, not applied.
