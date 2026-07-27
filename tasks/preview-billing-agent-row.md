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

## P2 — customer-facing consumption "оценка"  [DONE]
- [x] Root cause of the x3 the user SEES = `consumptionApps` (billing_consumption.go): `JOIN environments ... WHERE kind='App'` with NO ephemeral filter, so prod + pr-6 + pr-7 all listed and each priced. Feeds GetProjectConsumption + GetAccountSummary.
- [x] Fix = add `AND NOT e.is_ephemeral` (is_ephemeral BOOLEAN NOT NULL DEFAULT FALSE, mig 014 — safe). Previews are the free feature, so they vanish from the customer's own estimate; their cost already lands in the platform bucket (P1 adminCostOwnerOf). Godoc updated.
- [x] overhead-factor denominator (userNamespaces / billing_fullcost) UNTOUCHED — deliberately: removing previews there would reclassify them as shared infra and raise per-unit price for everyone.
- [x] OUT of scope (evidence): meterCountResource (billing_meter.go) also counts previews, but usage_records has ZERO `FROM` reads (write-only) and the meter is gated off by BillingEnabled=false. No user-facing harm now — noted, not fixed (minimal impact).
- [x] gofmt clean, go build/vet OK, go test ./internal/api PASS. Not-tested: no DB integration harness in repo; SQL filter mirrors the admin-layer is_ephemeral routing already covered by TestAdminCostOwnerOfRouting + mig 014. No Go-level seam to unit-test the WHERE clause.

## P3 — per-project agent-tasks row  [DONE]
Backend admin_costs.go + small FE:
- [x] `adminCostAgentTokens(ctx, from, to)` SQL: SUM cost_usd GROUP BY project_id JOIN projects+users (mirrors adminCostServiceDatabases; personal-team owner alias). project_id IS NULL rows stay only in the global card.
- [x] `injectAgentTokenRows` after splitSharedDatabaseCost, before flatten: ensureResource(kind="agent"), TotalCost=costUSD*USDToRUB, Revenue=AgentTokenRevenueRUB. Owner-less project -> platform bucket.
- [x] DESIGN = NON-FOLD (simplest honest). Extracted `rollupClient`; it skips kind=="agent" so agent NEVER enters project/client subtotals, total_cost, or reconciliation. Reconciliation/KPIs/overviewMoney untouched. Agent row still renders its own cost/revenue/margin. Matches existing invariant (billing_agent_tokens.go:69-73 "never folded into hardware-reconciled totals"). Tradeoff: parent subtotal excludes the agent child row (agent is a distinct kind, like unallocated) — acceptable; owner asked for a LINE, got it.
- [x] FE: localized label when kind==="agent" (threaded `t` through CostTree->ClientRows->ProjectRows); i18n key adminCosts.agentRow.label. NO recon-panel change needed (non-fold keeps recon pristine).
- [x] Test: TestRollupClientExcludesAgentFromSubtotals (agent priced+rendered, excluded from subtotals). go build+vet+test PASS. tsc 0 err, eslint clean.
- Residual: agent-only project (tokens but no k8s) shows parent 0/0/0 with a non-zero agent child — rare, honest. Live render god-gated (no /platform-admins bearer local).

## P4 — "move away from stable app-per-PR"  [DONE — owner chose OPT-IN previews]
Owner picked opt-in ("PR label / repo toggle") over shorter-TTL / lazy-boot / leave-as-is.

Chose the **PR label**, not a repo toggle, on evidence: there is NO repo edit endpoint (`git_repos` is INSERT-at-connect + DELETE only; router.go:383-385 has GET/POST/DELETE, no PATCH), so a per-repo column would need a migration + backend + the first-ever repo-edit UI and would still only help NEWLY connected repos. The label works for every EXISTING repo immediately (mendeleev included), build-agent-only, no migration, no schema, no frontend.

Implementation (build-agent only):
- [x] config: `PreviewEnvsRequireLabel` (`BUILD_PREVIEW_ENVS_REQUIRE_LABEL`, default **true** = opt-in) + `PreviewEnvLabel` (`BUILD_PREVIEW_ENV_LABEL`, default `preview`). The bool doubles as env-only kill switch back to auto-preview.
- [x] `pullRequestEvent`: parse `pull_request.labels` (full current set) + top-level `label` (the one that changed on labeled/unlabeled).
- [x] Action set += `labeled`, `unlabeled`.
- [x] `previewOptIn(cfg, ev)` gates CREATION only (`existing == nil`) — an already-created preview keeps tracking new commits, so a labeled PR still redeploys on every push. Case/space-insensitive match.
- [x] `previewOptedOut(ev)` = `unlabeled` AND removed label IS ours AND PR no longer carries it → immediate teardown via the EXISTING closePreviewEnv, so cost stops on un-label instead of waiting out the 7d TTL. Removing an unrelated label is a no-op (neither rebuild nor destroy).
- [x] Discoverability: one commit status on opened/reopened only ("add the 'preview' label to deploy one") — not on synchronize, to avoid per-push noise.
- [x] Fixed `TestPullRequestEventUnknownActionIgnored`, which used "labeled" as its example of an UNHANDLED action — my change invalidated that; now uses "assigned" and mirrors the real action set.
- [x] Fixed stale marketing copy (dict.ts ru+en) promising an env for "every pull request".
- [x] Tests: 3 new (label unmarshal, 9-case previewOptIn table, 6-case previewOptedOut table). gofmt/build/vet clean; FULL build-agent suite PASS; tsc clean; eslint clean; no forbidden unicode.
- Deploy note: default flips behavior on the next build-agent roll. `BUILD_PREVIEW_ENVS_ENABLED` is still the outer gate (default false).
- [x] LIVE VERIFIED end to end (commit ddb4667):
  - [origin] ddb4667 on origin/main; gate code present in origin's tree. Jenkins changeset for #620/#621 hid it because ea9b352 is a MERGE commit (first-parent attribution) -- ddb4667 IS an ancestor of the built d24d3a9.
  - [CI] build #621 SUCCESS 7m09s; ran `go test ./... -count=1` in build-agent and PASSED `build-agent/internal/server` (log line 625) -- my new tables actually executed, not just compiled.
  - [live] prod pod `dada-cloud-console-build-agent-84c6967658-4lp6z` running `build-agent:d24d3a98`, age 2m22s.
  - [live] prod env has `BUILD_PREVIEW_ENVS_ENABLED=true` and NO `BUILD_PREVIEW_ENVS_REQUIRE_LABEL`, so the code default (true = opt-in) is what is now in force. No config change was needed.
  - [code] gate completeness: `EnsurePreviewEnv` + `InsertCreatePreviewEnvOp` have exactly ONE production caller each, both inside openOrSyncPreviewEnv (server.go:423/430); gitops-agent only CONSUMES the op. No second path can create a preview, so opt-in cannot be bypassed.
  - Residual (honest): no live PR round-trip executed -- that would mean opening/labeling a PR on a real customer repo, which is an outward-facing action I did not take unprompted. First real labeled PR is the last mile.
- Pre-existing, NOT mine (evidence): `build-agent/internal/registry/nexus.go` is gofmt-dirty in the committed HEAD version and is unmodified in my worktree. Left alone, flagged separately.

## Constraints
- Trunk main, single line (M4 n/a for app code). Auto-push after each commit. Stage explicit paths (parallel worktrees). No source comments. New routes -> swag. Endpoints <300ms.
