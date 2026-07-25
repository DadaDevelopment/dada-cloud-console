# Economics: previews-are-free + per-project agent-tasks row

**Owner ask (2026-07-26):**
1. Move away from stable app-per-PR — an ignored PR must not pile x2/x3 cost on the user. Previews are a feature WE give.
2. Each project needs an "агентские задачи" (AI-token) row — we write agent spend there.
3. mendeleev: stop billing his two preview apps — he never asked for them.

**Ground truth (3 probes, all [code], billing also [live]):**
- Real billing = flat plan per org (yookassa/provider.go:82). Consumption/appCost/previews = **analytics view only, 0 real money**. "Un-bill" = pure view change. DO NOT touch yookassa/payments/billing_accounts.
- Abandoned PR is NOT forever: reaper kills ephemeral env ~7d after last commit (gitops-agent reaper, TTL 168h, every 10m); PR-close teardown too; cap 5/project. No scale-to-zero.
- Preview discriminator = `environments.is_ephemeral=TRUE` (migration 014) — authoritative, indexed, already in the owner query. No regex.
- agent_token_usage table has per-project `project_id` (mig 051) — [live] prod rows join cleanly. Global agent Card already exists (page.tsx:272-304); per-project row is a backend inject.

## P1 — previews route to platform bucket [D1]  (un-bill + previews-are-free, one fix)
Backend-only, admin_costs.go:
- [ ] `adminCostOwner` struct (+isPreview bool)
- [ ] `adminCostNamespaceOwners` query: SELECT e.is_ephemeral, scan it
- [ ] `adminCostOwnerOf`: if owner.isPreview -> platform bucket as `ns:<namespace>` (drives BOTH cost+revenue loops; platformCostOnly already blanks revenue). Update godoc.
- [ ] Test: extend TestAdminCostOwnerOfRouting with a preview case
- [ ] build + vet + go test ./internal/api/... ; commit + push
- Effect: artem fonbet row drops 572/1700 (3 merged copies) -> prod-only; pr-6/pr-7 become cost-only lines under Platform. Total cost unchanged, mischarged preview revenue gone.
- Known minor residual: per-preview DBs still size-split onto the customer project (projByID shares project_id, can't tell preview from prod DB). Preview DB disk is negligible vs pod compute; note, not blocking.

## P2 — customer-facing consumption "оценка" (billing_consumption / billing_fullcost)
- [ ] Exclude ephemeral namespaces from the customer informational view too (same principle). Verify overhead-factor denominator (userNamespaces) doesn't distort. Separate careful commit.

## P3 — per-project agent-tasks row  [DONE]
Backend admin_costs.go + small FE:
- [x] `adminCostAgentTokens(ctx, from, to)` SQL: SUM cost_usd GROUP BY project_id JOIN projects+users (mirrors adminCostServiceDatabases; personal-team owner alias). project_id IS NULL rows stay only in the global card.
- [x] `injectAgentTokenRows` after splitSharedDatabaseCost, before flatten: ensureResource(kind="agent"), TotalCost=costUSD*USDToRUB, Revenue=AgentTokenRevenueRUB. Owner-less project -> platform bucket.
- [x] DESIGN = NON-FOLD (simplest honest). Extracted `rollupClient`; it skips kind=="agent" so agent NEVER enters project/client subtotals, total_cost, or reconciliation. Reconciliation/KPIs/overviewMoney untouched. Agent row still renders its own cost/revenue/margin. Matches existing invariant (billing_agent_tokens.go:69-73 "never folded into hardware-reconciled totals"). Tradeoff: parent subtotal excludes the agent child row (agent is a distinct kind, like unallocated) — acceptable; owner asked for a LINE, got it.
- [x] FE: localized label when kind==="agent" (threaded `t` through CostTree->ClientRows->ProjectRows); i18n key adminCosts.agentRow.label. NO recon-panel change needed (non-fold keeps recon pristine).
- [x] Test: TestRollupClientExcludesAgentFromSubtotals (agent priced+rendered, excluded from subtotals). go build+vet+test PASS. tsc 0 err, eslint clean.
- Residual: agent-only project (tokens but no k8s) shows parent 0/0/0 with a non-zero agent child — rare, honest. Live render god-gated (no /platform-admins bearer local).

## P4 — "move away from stable app-per-PR" (architecture, owner decision)
Teardown already exists (7d). Levers: shorter TTL / scale-to-zero (useless for polling apps) / opt-in previews. Present options; do NOT build unprompted.

## Constraints
- Trunk main, single line (M4 n/a for app code). Auto-push after each commit. Stage explicit paths (parallel worktrees). No source comments. New routes -> swag. Endpoints <300ms.
