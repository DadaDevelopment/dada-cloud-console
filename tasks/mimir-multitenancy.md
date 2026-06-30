# Multi-tenant metrics via Grafana Mimir (per-tenant retention, default 15d)

Goal: per-tenant retention for USER-pushed cloud telemetry. Tenant = `projects.owner_id`
(org UUID) — the SAME value the write path already stamps as the authoritative `org_id`
label and the read path computes via `monitoringOrgLabel()`. Default retention 15d,
per-tenant override, all gitops-configurable.

Decision (user, 2026-06-30): Grafana **Mimir monolithic** + full build + cutover.
Why Mimir: only OSS engine with real per-tenant retention
(`compactor.blocks_retention_period` via runtime overrides). VictoriaMetrics OSS = single
global retention (per-tenant is enterprise).

## Topology after cutover

- **USER metrics** → Mimir (multitenant, X-Scope-OrgID = org_id). Blocks in Beget S3,
  WAL/local on a longhorn PVC. Monolithic single binary (`-target=all`).
- **INFRA metrics** (kube-pod scrape: container_*, node_*, kube_*, db sizes) → STAY on
  `monitoring-stack-prometheus`. These are scraped, not user-pushed, so they are NOT in
  Mimir. The backend keeps reading them from the infra Prometheus.
- **USER logs** → unchanged (ES ILM `dada-app-logs-ilm`, 15d).

Two read sources ⇒ backend needs **two Prometheus clients**:
- `h.prometheus` (existing) → infra Prometheus, used by `metrics.go` (container) +
  `databases.go` (db sizes). No tenant header.
- `h.userMetrics` (NEW) → Mimir, used by `monitoring_read.go` + `monitoring_alerts.go`,
  sends `X-Scope-OrgID: <org_id>` per request.

## Tenant header — where it goes

- WRITE (OTLP gateway): each request resolves ONE tenant from the API key
  (`gateway/resolver.go` → `res.tenant.OrgID`). Set `X-Scope-OrgID: org_id` on the
  remote-write POST (`gateway/server.go:216` → `prometheus.WriteClient.Write`). Harmless on
  plain Prometheus (ignored) ⇒ backward-compatible.
- READ (console): add `orgID` param to `prometheus.Client.QueryRange/QueryInstant/get`,
  inject `X-Scope-OrgID` when non-empty. User handlers pass the org; infra handlers pass "".
- VM direct-push (prometheus-agent on bootstrapped VMs, bypasses gateway): routed to Mimir
  via the `vm-metrics-write` ingress. These agents do NOT currently carry org_id and the
  console does not yet template a per-VM `X-Scope-OrgID` header ⇒ they land in Mimir's
  default tenant. FOLLOW-UP (flagged, not in this round): template
  `headers: { X-Scope-OrgID: <org> }` into each VM's prometheus-agent remote_write at
  provision time, so VM metrics are per-tenant too.

## Per-tenant retention knob (the gitops value)

Mimir `limits.compactor_blocks_retention_period: 15d` = the DEFAULT for every tenant.
Per-tenant overrides in a runtime-config ConfigMap:
```
overrides:
  <org-uuid>: { compactor_blocks_retention_period: 30d }
```
Edit ConfigMap, commit, push → Mimir reloads (runtime_config period). This is the
"configurable срок хранения", now genuinely per-tenant.

## Build order (respects manual image-pin deploy)

1. **dada-cloud code** (this repo): gateway write header; second Mimir read client + header;
   config for Mimir URL. `go build` + `go vet` + telemetry/prometheus tests green. Commit →
   Jenkins builds backend image.
2. **argo-infra infra**: S3Bucket claim (mimir-blocks) → secret in `monitoring`; Mimir
   monolithic app (StatefulSet + config + runtime-config + Service + PVC). `helm template`
   green. Commit → Mimir deploys parallel, receives nothing yet.
3. **Cutover** (after new backend image pinned): point gateway write + console
   userMetrics URL at Mimir; repoint `vm-metrics-write` ingress → Mimir. Infra Prometheus
   reverts to 7d (no longer carries user telemetry).

## S3 (Beget) facts

- Claim `kind: S3Bucket` (`platform.dada-tuda.ru/v1alpha1`), `spec.bucketName`, region ru1,
  `connectionSecret.{name,namespace}`. Endpoint `https://s3.ru1.storage.beget.cloud`.
- Connection secret keys: `access_key`, `secret_key`, `s3_url`, `bucket_name`.
- GOTCHA: Beget returns a GENERATED `bucket_name` (≠ display name) → Mimir must read it from
  the secret, not hardcode.

## Risks / flags

- New longhorn PVC for Mimir WAL (same SC as infra Prom/ES, should bind). Blocks in S3.
- Mimir monolithic memory footprint ~1–2Gi on a storage/over-committed cluster — size limits.
- Cutover gated on the new backend image being built+pinned (manual). Pre-image, Mimir runs
  empty; do not flip read/write URLs until the image is live.
- VM direct-push tenant header = follow-up (default tenant until then).
- Per-tenant retention is now real; cross-tenant query isolation enforced by Mimir.
</content>
