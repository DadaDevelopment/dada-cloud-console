# GOAL: Monitoring product → production-ready

Owner decisions (locked 2026-06-30):
- **Devices concept: REMOVED entirely** (frontend + backend). The real label-filtering
  plumbing (`source` and other labels) survives but is **generalized** into user-facing
  generic scopes/filters (group by pod/instance/any label, filter by label value).
- **Retention default: 15 days**, exposed as a configurable knob.
- **Dashboard tab:** try to fix the embedded Grafana iframe. If it works, native panels
  become optional. If Grafana stays broken, rip Grafana out and make the **native**
  dashboard far more flexible (libs allowed). Native flexible dashboard is built FIRST
  regardless — it is the spine and also fixes fast-start.

## Target state (definition of done)

1. **Easy user setup** — onboarding flow to start sending metrics/logs is clear and short.
2. **Fast & working ingest** — metrics→Prometheus, logs→ES; persistent; retention=15d,
   configurable.
3. **Working, fast UX** — dashboard + logs render FAST (no ~10s wait). Native panels
   render instantly from backend read-path; Grafana (if kept) behind a button / lazy.
4. **Configurable filters & scopes** — group metrics by any label (pod, instance…),
   filter by label value. Generic, no hardcoded "device".
5. **Full abstraction** — works on arbitrary metric/label sets, not a fixed schema.
6. **No devices** — zero "Device"/"N discovered" anywhere.

## Root causes found (from code map)

- **Weird chart (1→line@1, 1+2→line@2):** backend reads metrics as raw `avg(<name>)`
  (monitoring_read.go:460), no `rate()`. Cumulative counters render raw. metric-chart.tsx
  min/max-normalizes the window; on 2 points it's garbage. → counters need rate, chart
  needs proper multi-point/multi-series time rendering.
- **Devices leak:** hardcoded Device selector page.tsx:300-322 + getSources →
  backend `group by (source)`. → generalize to label scopes, kill device framing.
- **Grafana "Unable to find application file":** Grafana JS-bundle 404 behind reverse
  proxy. Server config in argo-infra (root_url / serve_from_sub_path / allow_embedding /
  version). → investigate argo-infra grafana values.

## Waves

### Wave 1 — Native flexible metrics/dashboard (spine) — covers goals 3,4,5,6 + chart bug
- [ ] Backend: `GetMonitoringMetrics` — counter rate() heuristic + `groupBy`/`filter`
      query params; return multi-series with label sets.
- [ ] Backend: new generic `GetMonitoringLabels` (label keys+values for scope/filter UI),
      replacing device-specific `GetMonitoringSources`.
- [ ] Frontend types/api: series-set shape, labels endpoint, drop source-as-device.
- [ ] Frontend metric-chart: multi-series, proper time axis, counter-aware, tooltips.
- [ ] Frontend metrics-panel: generic group-by + filter controls.
- [ ] Frontend page.tsx: remove Device selector + "N discovered".
- [ ] Verify: run app, send synthetic metrics, confirm dynamics + scopes + no devices.

### Wave 2 — Dashboard tab fast-start + Grafana decision — goal 3
DECISION (locked): RIP OUT Grafana iframe. Native flexible dashboard becomes the tab.
Rationale: investigator's top fix (serve_from_sub_path:true) is likely WRONG (root_url is
domain-root, not sub-path → would break assets). The error is Grafana's own bundle-skew
message; debugging the iframe across prod infra with no runtime DevTools evidence is guesswork.
Native path is instant (kills 10s wait), fully controlled, works on arbitrary data, removes a
fragile dependency, and matches the user's fallback branch. Do NOT touch grafana server config.
- [ ] Dashboard tab content = native flexible dashboard (reuse Wave-1 metrics engine, richer
      layout: multiple panels, group-by/filter, time range).
- [ ] Remove the Grafana <iframe> + its getGrafanaLink fetch from the tab.
- [ ] Keep "Открыть в Grafana" as an EXTERNAL link (new tab, direct SSO nav — embed bug
      doesn't apply to direct navigation). Backend grafanaEmbedURL/deep-link stays.
- [ ] Verify native dashboard renders instantly with dynamics + scopes.

### Wave 3 — Retention configurable (15d) — goal 2
EXACT targets (argo-infra kube-prometheus-stack values.yaml,
clusters/beget-prod/projects/platform/environments/prod/apps/kube-prometheus-stack/values.yaml):
- [ ] line 5023 `retention: 7d` → `15d`
- [ ] line 5027 `retentionSize: "5GiB"` → `"10GiB"`
- [ ] line 5148 PVC `storage: 8Gi` → `16Gi` (longhorn-cache; expansion may be blocked — verify at apply)
- [ ] update comment block 5134-5140 wording (7d → 15d)
- [ ] ES ILM 15d — locate ILM policy (elasticsearch-infra), set rollover/delete 15d
- NOTE: retention is a HELM VALUE = configurable knob (satisfies "настраиваемый"). Per-tenant
  retention NOT feasible on shared single-tenant Prometheus — global 15d, documented honestly.
- APPLY batched with Grafana infra change (both gitops→prod), AFTER Wave 1 verified.

### Wave 4 — Onboarding/setup polish — goal 1
- [ ] Review monitoring onboarding page; tighten copy + copy-paste snippet; remove device
      identity framing.

### Wave 5 — Final verification
- [ ] End-to-end: setup → ingest → see metrics(dynamics)+logs → scopes/filters → retention.
- [ ] Staff-engineer review pass.

## Review / results
(filled as waves complete)
