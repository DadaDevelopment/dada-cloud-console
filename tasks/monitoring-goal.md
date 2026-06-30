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

### Wave 3 — Retention configurable (15d), TRUE store separation — goal 2
TOPOLOGY VERIFIED 2026-06-30 then SPLIT. Full writeup: docs/runbooks/telemetry-retention.md.
Stores are now physically separate (user vs infra) for both metrics and logs.

DONE (argo-infra, branch console-migration):
- [x] NEW dedicated USER metric store: app `user-telemetry-prometheus` (local chart) — a second
      Prometheus CR (operator-reconciled, watches all ns), `enableRemoteWriteReceiver: true`,
      SCRAPES NOTHING (nil selectors), retention 15d (knob), own longhorn-cache PVC 8Gi.
- [x] Repoint user WRITE+READ → new store:
      - `cloud-console` global.shared.prometheusQueryUrl → user-telemetry-prometheus:9090. The
        OTLP gateway has no REMOTE_WRITE_URL so it falls back to this query URL → gateway WRITES
        also move. One value moves console-read + gateway-write together.
      - ingress `vm-metrics-write` (vm-observability) backend → user-telemetry-prometheus:9090
        (external VM prometheus-agents now push to the user store).
- [x] INFRA Prometheus `monitoring-stack-prometheus` reverted to 7d, re-framed infra-only. No
      longer carries user telemetry → no shared retention knob anymore.
- [x] USER logs ES ILM 15d: app `dada-app-logs-ilm` — Job PUTs ILM `dada-app-logs-policy`
      (delete @ retentionDays default 15) + index template for dada-app-logs-*. filebeat-* /
      dada-vm-logs-* untouched.
- Verified: helm template + helm lint + yaml parse green. Auto-syncs via tenant-apps ApplicationSet.
- Limitation: per-TENANT retention still N/A (user store is single shared Prometheus); global
  user-store 15d delivered. Per-tenant would need VictoriaMetrics/Mimir (not deployed).
- Storage flag: user store is a NEW longhorn-cache PVC (not the blocked in-place expansion);
  same SC infra Prometheus + ES already use. Watch longhorn commit on first sync.

### Wave 4 — Onboarding/setup polish — goal 1
- [ ] Review monitoring onboarding page; tighten copy + copy-paste snippet; remove device
      identity framing.

### Wave 5 — Final verification
- [ ] End-to-end: setup → ingest → see metrics(dynamics)+logs → scopes/filters → retention.
- [ ] Staff-engineer review pass.

## Review / results

- Wave 1 DONE (commit d30438c, pushed). Counter rate() fix (kills "value not dynamics"
  bug at the source), generic groupBy/filter scopes, /sources→/labels, devices removed
  FE+BE+i18n+onboarding, multi-series chart. Verified: go build+vet, tsc, eslint green.
  Live counter-dynamics verification pending deploy (needs real Prometheus).
- Wave 2 DONE (commit 42af9f5, pushed). Grafana iframe tab removed (kills "Unable to find
  application file" + 10s wait). Native dashboard = Overview+Metrics (instant). Grafana via
  header deep link. Dead i18n cleaned. Verified: tsc, eslint green.
- Wave 4 DONE (commit 58eea73, pushed). Ingest root-fix: monotonic OTLP sums get _total
  (CounterMetricName) so ANY custom counter is rate()d — fixes the exact test_counter flat-line
  case. Onboarding curl demo now climbs (date +%s) and shows real dynamics; all device wording
  gone from snippets. Verified: go build, telemetry tests, tsc, eslint green.
- Wave 3 retention: SAFE path chosen = bump time retention 7d→15d ONLY, leave retentionSize
  5GiB + PVC 8Gi (NO longhorn resize). Prometheus keeps min(15d, 5GiB). Reversible. GATED:
  argo-infra = prod gitops, awaiting user go-ahead.
- Wave 5 verification: needs the new console image deployed (CI builds; deploy = manual tag
  pin in argo-infra). Live counter-dynamics check pending deploy. GATED on prod-deploy decision.

- CI: build #249 (58eea73) FAILED — TestOpenAPICoverage: /labels route not in generated
  swagger spec (annotations present, embedded docs stale). MINE, owned. Fixed by regenerating
  internal/api/docs/swagger.json (commit 13d5f5c). Full `go test ./...` green locally. Awaiting
  CI rebuild confirmation.
- Retention: delegated to Claude chip task (task_1bfaf5b2) — user clarified user-pushed cloud
  telemetry retention is a SEPARATE store from cluster-pod scrape; make it gitops-configurable.
- Deploy: "пуш сам всё сделает" (auto-deploy on push) — no manual tag pin needed.

## Status: code waves shipped (main: d30438c, 42af9f5, 58eea73, +CI fix 13d5f5c).
Live dynamics verification follows auto-deploy. Retention separated into its own chip task.
