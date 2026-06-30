# 2026-06-29 Default project + single-project console navigation

User report: "создать проект не работает" (spinner hangs / nothing happens), and a
redesign: there should be ONE default project that exists immediately; if the user
has a single project the console lands INSIDE it (not on the projects overview); the
"create project" action lives in the top-bar dropdown (replacing the "Все проекты →"
link); the flat /projects overview is no longer needed because the dropdown switches.

Decisions (confirmed with user):
- Default project is auto-provisioned on the BACKEND.
- "Create" lives in the dropdown switcher.
- /projects overview is dropped as a landing → redirect into default project.

## Root-cause of "висит"
Manual create + navigate is correct in code for both god and normal users (personal-org
cascade makes the new project visible). The infinite spinner is the SSO token-getter
hanging (token expires between page-load GET and modal POST → silent refresh stuck →
fetch never fires → `submitting` never resets). Fix = hard timeout on apiFetch so any
hang surfaces as an error instead of a forever-spinner, AND route around the empty
state entirely via auto-default-project.

## Plan
- [ ] Backend: idempotent `POST /api/v1/projects/default` — returns the caller's default
      project, creating it (personal org = username, slug `<username>` sanitized, fallback
      `default-<hash>`) when they have zero. Reuses CreateProject insert logic. God users
      with zero projects also get one (so console always has a home).
- [ ] Frontend `apiFetch`: AbortController timeout (~20s) → throws on hang, no infinite spinner.
- [ ] Frontend: extract `CreateProjectModal` into `components/shell/create-project-modal.tsx`
      (shared by switcher + bootstrap).
- [ ] Frontend `ProjectSwitcher`: replace "Все проекты →" footer with "+ Создать проект"
      that opens the modal inline; on create → refetch list + route into new project.
- [ ] Frontend `ProjectProvider`: after list loads, if empty → call bootstrap default and
      route into it; expose `defaultProjectId`.
- [ ] Frontend `/projects` page → redirect into default/first project (overview dropped).
- [ ] Console root entry → land inside default project instead of /projects overview.
- [ ] Verify: create works, no infinite spinner, single project auto-lands, dropdown create works.

## Review
Done & verified (build + go test ./internal/api green; tsc + eslint clean):
- Backend `EnsureDefaultProject` (`POST /projects/default`), idempotent: returns the
  first visible project or provisions a default (personal org = username) when zero.
  Shared `insertProject` helper; `defaultProjectSlug` for a stable per-user slug
  (unit-tested). Route registered; swagger.json regenerated (coverage gate green).
- Frontend `apiFetch`: 30s AbortController timeout → hung request throws instead of
  spinning forever (root-cause mitigation for the "висит" symptom).
- Shared `CreateProjectModal` extracted; `ProjectSwitcher` footer is now
  "+ Создать проект" (was "Все проекты →") and refetches the list on create.
- `ProjectProvider` bootstraps the default project on empty list and routes into it;
  exposes `defaultProjectId` + `refetchProjects`.
- `/projects` overview replaced by a redirect into the default/first project.

Not verified in a live browser: needs Keycloak SSO + Postgres backend, which a local
preview can't faithfully exercise. Backend logic is unit-tested; frontend typechecks.
Remaining (out of scope / infra): the underlying SSO silent-refresh hang, if that was
the true cause, is only mitigated (timeout), not fixed in the auth layer.

---

# 2026-06-30 Metrics → Observability Dashboard rebuild (ECharts)

Scope (locked with user): backend+frontend together; land on **monitoring detail** page first
(richest data: discovery + groupBy + filter); **panel editor MVP** (add-panel dialog,
drag/resize grid, localStorage persistence).

Backend ceiling today: ranges 15m/1h/6h/24h; agg fixed (counter→sum/rate, gauge→avg);
no percentiles; no custom range. We lift this minimally.

## Phase 0 — Backend (Go) ✅
- [x] parseRange: flexible `range` (30m,2h,7d,12h) + absolute `from`/`to` unix; presets unchanged.
- [x] GetMonitoringMetrics: `agg` allowlist (avg|sum|min|max|count|p50|p90|p95|p99); percentiles→quantile(_by); default unchanged.
- [x] Unit tests parseRange + aggExpr; `go test ./internal/api/...` green; swagger regenerated.

## Phase 1 — Frontend foundation
- [x] Install echarts + react-grid-layout (+types). Local npmjs install.
- [ ] lib/cn.ts (clsx+twMerge).
- [ ] components/charts/echart.tsx: theme-aware (matchMedia), ResizeObserver, progressive, base option, dynamic ssr:false.

## Phase 2 — shadcn/Radix primitives
- [ ] tabs, dropdown-menu, popover, command, select wrapper.

## Phase 3 — Data layer
- [ ] types.ts + api.ts getMetrics opts (agg, from/to). useMetricsQuery hook (configurable poll, abort).

## Phase 4 — Chart kit
- [ ] line/area/stacked/bar/histogram/heatmap/scatter/gauge/sparkline/status-timeline builders + thresholds/annotations.

## Phase 5 — Dashboard shell
- [ ] Toolbar (range+custom, refresh, source, labels, groupBy, agg). KPI row (value+delta+sparkline+status). 12-col grid + panel chrome.

## Phase 6 — Metrics Explorer
- [ ] metric search, label filter, groupBy, aggregation wired.

## Phase 7 — Panel editor MVP
- [ ] Add-panel dialog (metric+viz+groupBy+agg), drag/resize/remove, localStorage save.

## Phase 8 — Persistence
- [ ] localStorage per project+resource: layout/filters/range/refresh/panels.

## Phase 9 — Wire-in
- [ ] Replace MetricsPanel(kind=monitoring) in monitoring/[appId] with new dashboard; vm/app keep old panel.

## Phase 10 — Verify
- [ ] frontend build + go test green; preview screenshots before/after; deliverables report.

## Review (Metrics rebuild) ✅
Done & verified (frontend tsc + eslint clean; `go test ./internal/api/` green; `next build` green;
ECharts kit rendered live in a throwaway preview route — all 11 viz types painted, screenshot captured).

Backend (Go):
- parseRange: flexible `range` (`<n>m|h|d|w`) + absolute `from`/`to`; presets byte-identical; adaptive step for long windows; 90d cap.
- GetMonitoringMetrics `agg` param (avg/sum/min/max/count/p50/p90/p95/p99), percentiles via PromQL quantile; allowlist-guarded; default behavior unchanged.
- Unit tests `metrics_range_test.go`; swagger regenerated.

Frontend (new):
- charts/: echart.tsx (theme-aware ECharts adapter, ResizeObserver, lazy theme re-init), theme.ts, format.ts, types.ts, builders.ts (line/area/stacked/bar/histogram/heatmap/scatter/gauge/sparkline/status-timeline + thresholds/annotations/dataZoom).
- metrics/: dashboard-types, use-metrics-query (seq-guarded poll), use-dashboard-state (localStorage persist), toolbar, kpi-row, panel, panel-grid (react-grid-layout@1.5.2), metrics-explorer, add-panel-dialog (editor MVP), monitoring-dashboard (orchestrator).
- ui/: tabs, popover, dropdown-menu, select (Radix + cn). lib/cn.ts.
- Wired into monitoring/[appId] metrics tab; removed duplicate panel from overview.

Deferred / out of scope: vm + app surfaces still use old MetricsPanel (deliberate, monitoring-first);
full class-based dark toggle for the whole console (charts already auto-follow prefers-color-scheme);
status-timeline + annotations have builders but no UI yet to configure them.
Not verified in a live browser end-to-end: needs Keycloak SSO + Prometheus data, which local preview
can't exercise — visualization layer proven with synthetic data instead.
