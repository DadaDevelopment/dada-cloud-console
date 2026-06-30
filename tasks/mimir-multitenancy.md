# Multi-tenant metrics via Grafana Mimir (per-tenant retention, default 15d)

## UPDATE: TENANT = project_id (per-project isolation) — code shipped 2026-06-30

The first cutover used tenant = `projects.owner_id`. The single-org collapse
(migration 022) made owner_id identical across nearly all projects, so EVERY
project folded into one Mimir tenant — per-tenant retention/limits/query-isolation
were effectively global. Fix: tenant = **project_id** on write AND read.

Decision (why project_id, not org): project is the real isolation boundary a
customer cares about (own retention, own ingest budget, hard query isolation).
org-level is meaningless while orgs are collapsed.

What changed:
- dada-cloud: gateway write X-Scope-OrgID = `res.tenant.ProjectID` (OTLP+JSON);
  console read `monitoringReadTenant()` = project_id, federated with the legacy
  tenant (`project_id|owner-or-anonymous`) so pre-cutover data stays readable
  (project_id label still scopes ⇒ no leak). Per-project Grafana datasource
  (`EnsureDatasource`) injects the tenant header for the embed. New env
  `GRAFANA_MIMIR_QUERY_URL`. (commit on main, build/vet/tests green.)
- argo-infra (console-migration): Mimir `tenant_federation.enabled: true`;
  runtime overrides `perTenantRetention`→`perTenantOverrides` (retention +
  ingestion_rate/burst + max series, keyed by project_id); VM push tenant =
  basic-auth username (`X-Scope-OrgID $remote_user`, shared cred → "anonymous");
  cloud-console gets `GRAFANA_MIMIR_QUERY_URL`.

VERIFIED LIVE (kubectl port-forward, real client code): pushed dada_e2e_probe to
tenantA(projA=42) + tenantB(projB=7); each tenant reads only its own series;
cross-tenant query EMPTY both ways ⇒ isolation holds. Federation read
(`tenantA|tenantB`) currently rejected live with "too many tenant IDs ... max: 1"
— that is the undeployed `tenant_federation.enabled` flag; works once applied.

NEEDS PROD APPLY (user triggers):
1. Build + pin the new backend image, then deploy cloud-console (manual tag pin,
   no image-updater) — activates project_id read/write + per-project datasource +
   GRAFANA_MIMIR_QUERY_URL.
2. argo sync Mimir + RESTART mimir-0 (tenant_federation is a static config flag,
   not runtime_config) so federated back-compat reads work.
3. argo sync vm-observability (new "anonymous" cred + $remote_user tenant). NOTE:
   existing VMs using the old `vmagent` cred will 401 until re-pointed to the
   "anonymous" user (same password) — coordinate the agent config flip.
4. Verify end-to-end through the DEPLOYED console read path (project A vs B), the
   remaining Not-tested gap (needs the new image live + two real projects).

FOLLOW-UPS (not blocking): per-PROJECT VM credential issuance (username =
project_id) at provision time in console/portainer-agent — until then VM metrics
land in "anonymous". User-VM LOGS are not per-tenant (filebeat→ES carries no
org_id); ES multi-tenancy is field/ILM-based, separate work.

---

## STATUS: SHIPPED + VERIFIED LIVE (2026-06-30)
Mimir deployed on beget-prod, healthy (/ready 200, S3 connected). Cutover applied:
gateway write + console read + vm-metrics ingress → Mimir; infra Prometheus → 7d.
Verified end-to-end: synthetic OTLP push as `probe-org` → read back 42; `other-org` →
empty (per-tenant isolation). Decision: KEEP Prometheus for infra scrape/rules/alert
(34 PrometheusRules, 17 SM, 7 PM, 1 AM) — Mimir can't scrape/consume those.
Live fixes during rollout: longhorn floor → emptyDir (history in S3); non-root crash →
activity_tracker.filepath=/data. Follow-ups: per-VM X-Scope-OrgID (VMs share `anonymous`);
Grafana embed datasource still on old Prometheus.

---


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
