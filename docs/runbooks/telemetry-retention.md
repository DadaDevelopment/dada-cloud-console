# Telemetry retention — where it lives & how to change it

USER cloud telemetry runs on a dedicated **multi-tenant Grafana Mimir** with **per-tenant
retention** (default 15d). Infra-pod telemetry stays on Prometheus. Verified live on
beget-prod 2026-06-30.

| Store | Holds | Retention knob (gitops, argo-infra) | Default |
|-------|-------|--------------------------------------|---------|
| **USER metrics** (Mimir) | OTLP gateway + external VM remote-write, per tenant | `apps/mimir/chart/values.yaml` → `defaultRetention` + `perTenantRetention.<org_id>` | **15d** (per-tenant) |
| INFRA metrics (Prometheus) | cluster-pod scrape, rules, alerting | `apps/kube-prometheus-stack/values.yaml` → `prometheus.prometheusSpec.retention` | 7d |
| **USER logs** (ES ILM) | `dada-app-logs-*` | `apps/dada-app-logs-ilm/chart/values.yaml` → `retentionDays` | **15d** |
| INFRA logs (ES ILM) | `filebeat-*` | `apps/elastic-stack/values.yaml` | 7d/14d |

## Why two metric stores (not redundant)

Mimir does **not** scrape and does **not** consume PrometheusRule CRs — it's a
storage+query backend fed by remote-write. The kube-prometheus-stack Prometheus does the
scraping (17 ServiceMonitors, 7 PodMonitors, node/kube-state/cAdvisor), evaluates 34
PrometheusRules, and drives Alertmanager. None of that moves to Mimir for free. Decision
(2026-06-30): keep the split — Prometheus = infra scrape/rules/alert (7d); Mimir = the
multi-tenant product store.

## Mimir topology

- App `mimir` (ns monitoring): monolithic single binary, metrics-only target set
  (no ruler/alertmanager → one S3 bucket suffices), `multitenancy_enabled`.
- Tenant = `org_id` (`projects.owner_id`) via the `X-Scope-OrgID` header.
- Blocks in Beget S3 (`S3Bucket` claim `dada-mimir-blocks` → secret `mimir-s3-credentials`;
  Mimir reads the generated bucket name from the secret). Local `/data` is an **emptyDir**
  (the longhorn-cache disks are at the minimal-available floor; history is durable in S3,
  only the last ~2h head is at risk on a pod reschedule).
- Service `mimir.monitoring.svc:8080` — push `/api/v1/push`, query `/prometheus`.

### Write path
- OTLP gateway (`backend/cmd/gateway`): `PROMETHEUS_REMOTE_WRITE_URL` →
  `http://mimir.monitoring.svc:8080/api/v1/push`; the gateway stamps
  `X-Scope-OrgID = tenant.OrgID` per request.
- External VM agents → ingress `vm-metrics-write` (app `vm-observability`): rewrites
  `/api/v1/write`→`/api/v1/push` and injects `X-Scope-OrgID: anonymous` (default tenant).
  **Follow-up:** template a per-VM `X-Scope-OrgID` at provision time so VM metrics are
  per-tenant too (today they share the `anonymous` tenant).

### Read path
- Backend has a SECOND client `userMetrics` (`prometheus.NewMultitenant`) used ONLY by the
  monitoring product reads (`monitoring_read.go`, `monitoring_alerts.go`); it sends
  `X-Scope-OrgID = caller org_id`. `USER_METRICS_QUERY_URL` →
  `http://mimir.monitoring.svc:8080/prometheus`.
- Infra/container/db reads (`metrics.go`, `databases.go`) keep the plain client →
  `monitoring-stack-prometheus`.

## How to change retention

| Want to change | Edit (argo-infra) | Field |
|----------------|-------------------|-------|
| User metric retention — ALL tenants | `apps/mimir/chart/values.yaml` | `defaultRetention` |
| User metric retention — ONE tenant | `apps/mimir/chart/values.yaml` | `perTenantRetention.<org_id>: 30d` |
| User log retention | `apps/dada-app-logs-ilm/chart/values.yaml` | `retentionDays` |
| Infra metric retention | `apps/kube-prometheus-stack/values.yaml` | `prometheus.prometheusSpec.retention` |

Commit + push to `console-migration` → ArgoCD applies. Mimir hot-reloads `runtime.yaml`
(per-tenant overrides) every 15s — no restart. `defaultRetention` change rolls the pod.

## This is genuinely per-tenant

Unlike a single Prometheus (one global retention), Mimir scopes retention per
`X-Scope-OrgID`. Verified: a synthetic push as tenant `probe-org` is invisible to
`other-org` (full isolation). So "настраиваемый срок хранения" is now per-organization.
