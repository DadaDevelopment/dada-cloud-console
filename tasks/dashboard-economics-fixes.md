# Dashboard / economics fixes — plan

Grounded 2026-07-25. Four asks from owner + two visible bugs.

STATUS 2026-07-25: A + C + D SHIPPED (in-repo, tsc/eslint/go build/vet/test all green; live numbers verify post-deploy). B = owner-decided, architecture-blocked on external agent-sync-hub token reporting — present decision before coding.

## A. Hardware spend transparency (NOT a correctness bug)
Finding: `Железо (30д) = 13 194 RUB` already equals the real Beget invoice.
Cached `beget:clusters` [live]: argo cluster 968 + Prod k8s 12226 = 13194. `hardware_source=beget_api`.
Per construction (admin_costs.go:227 `scale=hardwareTotal/rawTotal`) client costs already SUM to the hardware bill.
Problem is visibility: page shows only top-5 loss-makers (which are MARGINS, not costs) → user can't see 13194 decompose. Backend already returns `hardware_source`, `hardware` node breakdown, `scale_factor`, `opencost_raw_total`, full `clients`, `unallocated`.

Tasks:
- [x] Frontend `admin/costs/page.tsx`: methodology `<Card>` — model explanation + reconciliation ledger (Σclients + unallocated = totalExpenses vs hardware bill, Δ with green "сходится" when |Δ|<1) + params line (opencost_raw_total, scale_factor). Guarded on `!isLoading && data?.available`.
- [x] `CostTree` totals footer (`<tfoot>` Итого row: Σ cost/revenue/margin over clients).
- [x] Overview money card → link `adminOverview.money.fullBreakdown` to `/admin/costs` (discoverability).
- [x] i18n ru/en (admin-costs.ts method.* + table.total; admin-overview.ts money.fullBreakdown).
- [ ] POST-DEPLOY: confirm reconciliation Δ≈0 (sums to 13194) + revenue non-zero on live console.

## B. Tariff agent runs + dashboard  (DECIDED 07-25)
OWNER DECISION:
- UNIT = **tokens**, from BOTH agent systems: (1) in-console chat agent (agent_chat.go → ai-gateway/LiteLLM), (2) workspace/cloud-task agents **agent-sync-hub**.
- PRICE = **cost-plus × 2.7** (real gateway token cost × existing markup).
- SCOPE = **straight into user invoices** (build usage_records → invoice read-path now) + dashboard.

Finding: `usage_records` written by billing_meter but READ BY NOTHING → invoice read-path does not exist yet. Must build it. agent_chat_messages has no token counts yet.

Tasks (after B research maps surface):
- [ ] Capture prompt+completion tokens per LLM call. Source of truth TBD by research: either (a) LiteLLM's own spend/usage tracking (per-key/metadata), or (b) plumb `usage` from gateway responses into agent_chat.go + agent-sync-hub. Prefer single source if LiteLLM already records it per org.
- [ ] Attribute tokens to org (agent_chat has org_id; agent-sync-hub attribution TBD).
- [ ] Meter into `usage_records` (resource `agent_tokens`, split prompt/completion or total) per org/period — extend billing_meter.go MeterUsage.
- [ ] Price cost-plus × 2.7: need per-model gateway cost table (LiteLLM has model prices). Build read-path usage_records → invoice line item.
- [ ] Dashboard: admin economics card (tokens + agent revenue), and agent revenue flows into total_revenue so margin reflects it.
- [ ] NOTE latent risk: charging real money on unverified numbers. Meter+display must be sanity-checked against LiteLLM spend before first real charge.

## C. "Новые приложения в день" current-day spike (BUG)
Root cause [code]: `resource_snapshots` is one row/resource (conflict `(project_id,environment_id,kind,name)`), `last_synced_at` bumped every ~30s reconcile (UpdateLiveStatus). overviewDynamics uses `min(last_synced_at)` as first-seen → == now for every live app → all land in today. Table has no creation ts.

Tasks:
- [x] Migration 049: add `resource_snapshots.first_seen_at timestamptz NOT NULL DEFAULT now()` + backfill from builds + index.
- [x] NO agent code change needed. `first_seen_at` appears in NO writer's `ON CONFLICT ... DO UPDATE SET`, so `DEFAULT now()` freezes at true first insert automatically; the 30s reconcile upsert never bumps it. gitops/portainer agents need no redeploy.
- [x] Backfill: `UPDATE ... SET first_seen_at = min(builds.created_at)` per (environment_id, app_name) where earlier than default. Spreads real history; buildless legacy apps keep migration-day default (one-time artifact, scrolls out of ≤90d window). Idempotent for non-tx runner.
- [x] overviewDynamics new-apps query: `to_char(first_seen_at,'YYYY-MM-DD'), count(*) GROUP BY 1`, dropped the min(last_synced_at) proxy. Comment updated.
- [ ] POST-DEPLOY: confirm current-day bucket no longer spikes to 50+.

## D. "28 сломаны" headline vs honest panel "2" (BUG)
Root cause [code]: frontend `admin/page.tsx:87` `broken = appsTotal - ready` (naive non-Ready). Honest panel `overviewNotReadyApps` (strict live predicate, bb3d757) yields 2. Headline never got the fix.

Tasks:
- [x] Backend: extracted `brokenAppSnapshotPredicate` const (live_source=k8s, phase NOT IN Ready/Stopped/Orphaned, last_synced_at<10m) shared by BOTH the honest count and `overviewNotReadyApps` — single source, cannot drift.
- [x] `overviewApps` struct now returns `ready` + `broken`; overviewProjects fills them (ready from by_phase, broken from count query on the shared predicate).
- [x] Frontend `admin/page.tsx`: headline uses `apps.broken` / `apps.ready` from backend, not `total - ready`.
- [x] Guard-by-construction: honest broken count and not_ready list use the SAME predicate const → equal by definition.

## Order
D (small, clear) → A (frontend transparency) → C (migration+agent+backfill) → B (after decision).  [A/C/D done 07-25]
