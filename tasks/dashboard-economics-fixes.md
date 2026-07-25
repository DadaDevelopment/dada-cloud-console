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

GROUNDED 07-25 (code + ADR-015 read):
- Two agent systems, TWO metering points (not one):
  - **In-console chat agent** [agent_chat.go → agentchat.RunTurn → llmchat.Client] POSTs `{gateway}/v1/chat/completions` `Bearer sk-dada`, single shared key/model from config. Streams (SSE). Console is BOTH caller and biller: it knows org_id/user_sub/project in-context and gets the response. → **self-meterable in-repo now**, no gateway-config dependency. `llmchat.StreamResult` has NO usage field today → must add `stream_options.include_usage=true` + parse trailing usage chunk. A turn = ReAct loop = several LLM calls → sum usage across the loop.
  - **agent-sync-hub (cloud-task)** runs `claude -p` (Claude Code headless), EXTERNAL service, NOT through the gateway → invisible from this repo. DEFERRED to spec chip `task_3d6e95dc` (hub reports its own token/project/resource usage into the same ledger).
- ADR-015 collision: cost-plus **×2.7 markup = escape-trigger #2** (money axis BYOK/post-hoc → managed-key/markup = "financial transaction system, provider-invoice reconciliation"). B must be first-party ledger + pricing, NOT a LiteLLM-management-plane feature (ADR Decision #12 rejects that).
- OWNER 07-25: charge SVERKA-first? NO — **"сразу в счета"** (charge immediately). Still surface a reconciliation view so drift is visible.
- Invoice read-path today: `billing.go:190-195` `invoicePreview.amount = plan.PriceRUB` flat, no line items. `usage_records` written by billing_meter, read by nothing.

### B1 — capture + observe (in-repo, in-console agent only) — SHIPPED 83ddfd2 (07-25)
- [x] `llmchat`: send `stream_options.include_usage=true`; parse trailing `usage` (prompt/completion/total) + `model` into StreamResult.
- [x] `agentchat.RunTurn`/`ResumeTurn`: accumulate usage across the loop's LLM calls, return it (new `Usage` struct).
- [x] Migration 051 `agent_token_usage` ledger (org_id, project_id, env_id, user_sub, model, prompt/completion/total_tokens, cost_usd frozen, platform_request_id/cloud_task_id partial-unique idempotency, source default console_chat, created_at). Shared by BOTH agent systems (hub writes source=hub later).
- [x] `agent_chat.go`: after RunTurn/ResumeTurn, `recordAgentTokenUsage` writes ledger row keyed by caller org (no-op if total<=0, fail-soft log on error).
- [x] Pricing table `billing/pricing/agent_tokens.go`: per-model USD cost + `AgentTokenRevenueRUB(cost, fx, markup)`; unit-tested.

### B2 — charge (wire into invoices) — SHIPPED 1db80b7 (07-25)
- [x] Per-model provider price = in-repo USD table (default gateway models: sonnet 3/15, haiku 0.8/4; fallback = most expensive). FX = config `AGENT_TOKEN_USD_RUB_RATE` (conservative lower-bound default 80) × `AGENT_TOKEN_MARKUP` (2.7), applied at READ time so re-pricing needs no migration.
- [x] `billing.go` invoicePreview: agent_tokens line item, folded into month total (charge-immediately). Fail-soft: ledger error drops the line, invoice still 200.
- [x] Admin `/costs`: separate `agent_tokens` economics block (actual ledger revenue/cost/margin over reporting window) + frontend card. Kept OUT of hardware-reconciled infra totals so ask-A reconciliation stays intact.

### B3 — hub convergence (blocked on chip `task_3d6e95dc`)
- [ ] Hub reports `claude -p` usage into `agent_token_usage` (source=hub) per the chip's spec; idempotent on cloud_task_id/platform_request_id.

CHECKPOINT: B1+B2 shipped. Ledger + read-time pricing + invoice line + admin card live on main. Await POST-DEPLOY live-verify (ledger fills on real chat turn, invoice line appears, admin card shows). B3 waits on the hub chip.

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
