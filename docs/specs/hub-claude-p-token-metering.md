# Spec: Token metering for `claude -p` runs in agent-sync-hub / DadaAgent

Status: IMPLEMENTED on BOTH sides. Console: ingest handler + route + migration 053 (verified — see §11). Hub (agent_sync_hub): claude report captures cache-creation + model, CallbackClient.send_usage posts to the `/usage` route, cloud_task_runner emits one metered record per claude run keyed `ct-<task>-<run>-<model>`; hub unit + e2e-contract tests green, and the live prod auth+tenancy wire smoked `200 {stored:false}` against a real cloud_task with zero ledger pollution (see §11). Residual = hub redeploy + one real claude run to exercise the final INSERT.
Author: platform
Verified: `claude -p --output-format json` fields captured from a real run of Claude Code **2.1.215** (local); auth gate + migration numbering checked against repo `[code]`.
Related: [ADR-015 AI Gateway Runtime & Data-Plane](../adr/ADR-015-ai-gateway-runtime-data-plane.md), migration `051_agent_token_usage.sql`, `053_agent_token_usage_cloud_task_multi.sql`, `backend/internal/dadagent/`, `backend/internal/api/webhooks_dadagent.go`

## 1. Problem

Dada Cloud bills agent token usage into user invoices (unit = tokens, price = provider USD cost x FX x markup, summed at invoice time). Two agent systems produce that spend:

1. **In-console chat agent** — routes through the ai-gateway (LiteLLM, `sk-dada` keys). Metered via the LiteLLM success callback into the platform ledger per ADR-015, idempotent on `platform_request_id`. **Already shipped for the console side** as `agent_token_usage` (mig 051, commit 83ddfd2), currently written only by `recordAgentTokenUsage(...)` with `source='console_chat'`.
2. **agent-sync-hub / DadaAgent** — the cloud-task workspace agent, reached from the console over `DADA_AGENT_BASE_URL`. It runs `claude -p` (Claude Code headless) per cloud-task and **does not route through the gateway**, so its spend is invisible to the console and cannot be metered from the console repo.

This spec covers capturing and reporting `claude -p` token usage from the hub so it lands in the **same** `agent_token_usage` ledger, keyed and priced compatibly with the gateway path.

## 2. Grounding (what already exists — verified in `dada-cloud`)

The ledger the hub must feed already exists and is the invoice source of truth. Do not invent a parallel table.

`agent_token_usage` columns (mig 051):

```
source              TEXT NOT NULL DEFAULT 'console_chat'   -- discriminator: 'console_chat' | 'cloud_task'
org_id              TEXT
project_id          UUID
env_id              UUID
user_sub            TEXT
model               TEXT NOT NULL
prompt_tokens       BIGINT
completion_tokens   BIGINT
total_tokens        BIGINT
cost_usd            NUMERIC(14,6)     -- FROZEN provider USD at write time; FX+markup applied at invoice
platform_request_id TEXT              -- UNIQUE partial index (WHERE NOT NULL)
cloud_task_id       TEXT              -- UNIQUE partial index (WHERE NOT NULL)
created_at          TIMESTAMPTZ DEFAULT now()
```

Verified facts that shape the design:

- **Invoice = `SELECT SUM(cost_usd), SUM(total_tokens) FROM agent_token_usage WHERE org_id=$1 AND created_at >= from AND created_at < to`**, then `RevenueRUB = cost_usd * usd_rub * markup` at read time (`billing_agent_tokens.go`). Consequence: `period` is **not** a stored/payload field — it is derived from `created_at`. The row's `created_at` decides which invoice month it lands in.
- **The ledger stores provider USD frozen at write**; FX + markup stay out of the ledger (`recordAgentTokenUsage` doc). Consequence: whatever the hub reports as cost is treated as the raw provider USD and must not already include markup.
- **console_chat appends one row per turn**, `platform_request_id`/`cloud_task_id` left NULL, summed at invoice. The ledger is already an append-many-then-SUM model, not a running-total-upsert model.
- **The hub does not receive `project_id`/`org_id`.** The console→hub cloud-task payload (`cloud_tasks.go`) sends only `cloud_task_id`, `skill_id`, `repo{full_name, install_token}`, `params`, `callback.url`. The "repo + tokens" the console sends is repo + **GitHub App install token** — not usage tokens. Consequence: the console must resolve tenancy from `cloud_task_id`; the hub cannot be the source of `project_id`/`org_id`.
- **The hub already holds a durable correlation id per run.** For the runs/autofix path the agent mints `cloud_task_id` and the console reads it back via `GET /v1/runs/{id}` (`GetRun`) precisely because the callback webhook keys on it (`webhooks_dadagent.go` `correlationKey()`: `intent_id` else `cloud_task_id`). For the intent path the console mints `cloud_task_id` and sends it in the payload. **Either way the hub holds exactly the string the console's existing webhook correlates on.** The usage reporter reuses that same key — no new correlation id is needed.
- **Auth pattern exists.** The status/artifact webhook is bearer-gated by JWKS and accepts only `azp=dada-agent` (`DadaAgentWebhook`). The usage callback reuses this exact gate.

## 3. Design decisions

- **D1 — One ledger, discriminated by `source='cloud_task'`.** The hub feeds `agent_token_usage`, not a new table. Both agent systems land in one ledger, invoiced by the same SUM.
- **D2 — Callback, not direct DB write.** The hub is a separate service/repo with no console DB credentials, and tenancy resolution + pricing policy must stay console-owned. The hub POSTs usage to a console ingest endpoint (mirrors why console_chat writes in-process but the hub cannot). Rejected: direct write (crosses the trust + repo boundary), and trusting hub-supplied `project_id`/`org_id` for billing (a buggy/compromised hub could misattribute spend).
- **D3 — `platform_request_id` is the universal idempotency anchor**, for both systems, exactly as ADR-015 mandates ("Billing idempotency MUST use `platform_request_id`"). The hub mints it **deterministically** as `ct-<cloud_task_id>-<seq>-<model>` so retries are double-safe without a DB round-trip. `cloud_task_id` is carried as a non-unique attribution/grouping column.
- **D4 — Per-(invocation, model) immutable rows, aggregated at read.** One `claude -p` invocation is one billable unit but emits a `modelUsage` map (a run can span main + subagent models, §4), so it produces one row **per model**. A cloud-task that calls `claude -p` several times produces several such rows sharing `cloud_task_id`, summed at invoice. This matches the existing append-and-SUM write pattern and gives the best crash/partial-capture story (see §7). **Requires migration `053` to relax `UNIQUE(cloud_task_id)` → non-unique** (written, see §8).
- **D5 — Claude's own `modelUsage[m].costUSD` is the authoritative provider cost.** `claude -p` computes exact per-model cost including cache read/write pricing; the console's in-repo price table (`pricing.AgentTokenCostUSD`) may not cover every Claude model or cache pricing. So the cloud_task path stores the hub-reported per-model `costUSD` verbatim into `cost_usd` (unlike console_chat, which prices from tokens). The rows for one invocation sum to its `total_cost_usd`. Tokens are carried for display/audit. Both sources land a comparable `cost_usd`; the invoice SUM stays uniform.

## 4. Source of truth per run (point 1)

Verified against a real run — Claude Code **2.1.215**, `claude -p "…" --output-format json`, exit 0. Trimmed terminal object (`type:"result"`, `subtype:"success"`):

```json
{
  "type": "result", "subtype": "success", "is_error": false,
  "num_turns": 1, "stop_reason": "end_turn", "session_id": "…",
  "total_cost_usd": 0.212229,
  "usage": {
    "input_tokens": 3, "output_tokens": 4,
    "cache_creation_input_tokens": 35360, "cache_read_input_tokens": 0,
    "cache_creation": { "ephemeral_1h_input_tokens": 35360, "ephemeral_5m_input_tokens": 0 },
    "server_tool_use": { "web_search_requests": 0, "web_fetch_requests": 0 },
    "service_tier": "standard"
  },
  "modelUsage": {
    "claude-sonnet-4-6": {
      "inputTokens": 3, "outputTokens": 4,
      "cacheReadInputTokens": 0, "cacheCreationInputTokens": 35360,
      "costUSD": 0.212229, "contextWindow": 200000, "maxOutputTokens": 32000
    }
  }
}
```

Load-bearing facts from the real output:

- **There is no top-level `model` field.** The model is the *key* in `modelUsage`. A single `claude -p` run can span several models (main model + subagents on cheaper models), so `modelUsage` is a map, one entry per model used. The `model` ledger column comes from this key.
- **`total_cost_usd` == Σ `modelUsage[*].costUSD`** (verified on the single-model run: 0.212229 == 0.212229). This is the authoritative provider USD, already cache-discounted — do **not** re-derive cost from tokens.
- **Cache-creation dominates small runs.** The trivial "reply ok" prompt cost $0.21 on 35 360 cache-creation input tokens (Claude Code's own system prompt being cached). The reported cost already prices cache read/write; the coarse token count is display/audit only.
- Also present: `usage.cache_read_input_tokens`, per-TTL `usage.cache_creation`, `num_turns`, `session_id`, `subtype`/`is_error`/`stop_reason`, `permission_denials`, `usage.iterations[]`.

**One ledger row per (invocation, model)** — iterate `modelUsage`; for each model `m`:

- `model             = m` (the map key)
- `prompt_tokens     = modelUsage[m].inputTokens + cacheReadInputTokens + cacheCreationInputTokens`
- `completion_tokens = modelUsage[m].outputTokens`
- `total_tokens      = prompt_tokens + completion_tokens`
- `cost_usd          = modelUsage[m].costUSD` (per-model; the rows of one invocation sum to `total_cost_usd`)

Usually `modelUsage` has one entry → one row per invocation. The per-model split keeps the ledger `model` column truthful and preserves per-model economics for the admin costs view; the invoice SUM is unaffected (Σ rows == `total_cost_usd`).

**Streaming vs buffered:** for accounting, `--output-format json` (one buffered result object, verified above) is sufficient and simplest. `--output-format stream-json` emits incremental events **plus** a terminal `result` event carrying the same cumulative `usage` + `modelUsage` + `total_cost_usd`. In both modes the **final result object is the source of truth**; per-turn deltas are ignored for billing. Use `stream-json` only if the hub already needs live progress or wants partial-usage salvage on abnormal exit (§7).

> Residual version gap (M2): fields above are verified against local **2.1.215**. The hub pins its own Claude Code version; a build could rename `total_cost_usd`→`cost_usd` or omit `modelUsage`. Before coding, run `claude --version` in the hub image and diff one real `--output-format json` sample against the shape above; make the parser accept `total_cost_usd || cost_usd`, and fall back to `usage` + a single inferred `model` when `modelUsage` is absent.

## 5. Attribution & correlation (point 2)

- The hub tags every `claude -p` invocation with the `cloud_task_id` it already holds for that run (intent-path: console-minted, received in payload; runs/autofix-path: agent-minted, the same value the console read back via `GetRun`).
- The hub does **not** send `project_id`/`org_id` (it does not reliably have them). The console ingest endpoint resolves `project_id`, `env_id`, and `org_id` from `cloud_task_id` using the `cloud_tasks` row + the existing project→org mapping billing already uses. Any tenancy fields in the payload are optional hints and are overwritten by the console-resolved values (D2).
- Resolution key: the same lookup the status webhook uses (`cloud_tasks.intent_id = $cloud_task_id`, which holds the console UUID for the intent path and the agent id for the runs path). A `cloud_task_id` that resolves to no row → `404`, row not written, hub retries/dead-letters.

## 6. Reporting contract (point 3)

New console endpoint, sibling to the existing webhook, same auth (`azp=dada-agent`, JWKS bearer). Reuses the hub's existing callback config channel (add `CLOUD_TASK_USAGE_URL`, or reuse `CloudTaskCallbackURL` base + a `/usage` suffix).

```
POST /api/v1/webhooks/dadagent/usage
Authorization: Bearer <keycloak token, azp=dada-agent>
Content-Type: application/json
```

One POST carries **one ledger row = one (invocation, model)**. A single-model invocation is one POST; a multi-model invocation is N POSTs, each independently idempotent. (An array batch body is an optional later optimization, out of MVP scope.)

```json
{
  "platform_request_id": "ct-3f1c…-2-claude-sonnet-4-6",  // idempotency anchor (ADR-015): ct-<cloud_task_id>-<seq>-<model>
  "cloud_task_id": "3f1c…",                   // attribution key → console resolves org/project/env
  "provider_attempt_id": null,                 // ADR-015 field; reserved, null at MVP
  "source": "cloud_task",
  "model": "claude-sonnet-4-6",                // the modelUsage map key for this row
  "prompt_tokens": 35363,                      // inputTokens + cacheReadInputTokens + cacheCreationInputTokens
  "completion_tokens": 4,
  "total_tokens": 35367,
  "cache_read_input_tokens": 0,                // optional audit detail (already inside prompt_tokens)
  "cache_creation_input_tokens": 35360,        // optional audit detail (already inside prompt_tokens)
  "cost_usd": 0.212229,                         // modelUsage[model].costUSD — provider USD for THIS row; Σ rows == run total_cost_usd
  "num_turns": 1,                               // optional audit
  "occurred_at": "2026-07-25T10:22:04Z"         // → stored as created_at; decides billing period
}
```

Console ingest logic:

1. Verify bearer, require `azp=dada-agent` (reuse `DadaAgentWebhook` gate — same `agentVerifier`).
2. Resolve `project_id`, `env_id`, `org_id` from `cloud_task_id`; `404` if unknown (hub dead-letters/retries).
3. `INSERT INTO agent_token_usage (source, platform_request_id, cloud_task_id, org_id, project_id, env_id, model, prompt_tokens, completion_tokens, total_tokens, cost_usd, created_at) VALUES ('cloud_task', …, cost_usd, occurred_at) ON CONFLICT (platform_request_id) DO NOTHING`.
4. `200 {"ok": true}` on insert **or** conflict (idempotent).

Field alignment (ADR-015 ledger ↔ mig 051 columns ↔ this payload):

| ADR-015 identifier | 051 column | Payload field | Notes |
|---|---|---|---|
| `platform_request_id` | `platform_request_id` (UNIQUE) | `platform_request_id` | Billing idempotency anchor, both systems |
| `provider_attempt_id` | — (add later if needed) | `provider_attempt_id` | Reserved; for future per-attempt accounting |
| (correlation) | `cloud_task_id` | `cloud_task_id` | Hub's attribution key; console resolves tenancy from it |
| — (no top-level field) | `model` | `model` | the `modelUsage` map key; one row per model |
| usage | `prompt/completion/total_tokens` | same | cache tokens folded into `prompt_tokens` |
| observed cost | `cost_usd` | `cost_usd` | = `modelUsage[m].costUSD`; Σ rows == run `total_cost_usd`, stored frozen |
| — (derived) | `created_at` | `occurred_at` | period derived from this, never sent |

## 7. Idempotency, retries, crash (point 4)

- **Idempotent** on `platform_request_id` via `ON CONFLICT DO NOTHING`. Deterministic `ct-<cloud_task_id>-<seq>-<model>` means the same (invocation, model) always maps to the same row — hub retries and console dupes are both no-ops.
- **Retries:** the hub retries the POST with exponential backoff; safe by construction.
- **Crash after `claude -p` finished, before/ during POST:** the hub writes the parsed usage record to a small **local durable spool** (append file / embedded KV keyed by `platform_request_id`) **before** POSTing, and marks it acked on `200`. A startup + periodic flusher re-POSTs unacked spool entries. So a crash between capture and delivery still bills on recovery.
- **Crash during `claude -p` (no terminal result emitted):** that invocation's tokens exist only on Anthropic's side and are lost to the ledger. MVP accepts this — the loss is bounded to at most the single in-flight invocation, and the cloud-task's terminal state is still set independently by the existing status webhook. Hardening option: run `stream-json` and tally partial `usage` from streamed message events, emitting a partial/`failure`-tagged row on abnormal exit.
- **Reconciliation (optional, ADR-015 parity):** a periodic job flags any `cloud_tasks` row in a terminal `completed` state with zero `agent_token_usage` rows for its `cloud_task_id` (missing-callback), mirroring ADR-015's reconciliation event.

## 8. Per-invocation vs per-task-aggregate (point 5)

Two levels of aggregation exist:

- **Within one `claude -p` invocation:** Claude Code already aggregates all internal turns/tool-calls (`num_turns`) into one cumulative `usage` + `total_cost_usd`. So one invocation is naturally one aggregate unit — the hub never has to sum internal turns itself.
- **Across invocations in one cloud-task:** the hub run-loop may call `claude -p` several times (e.g. plan → fix → test). **Decision (D4): one row per (invocation, model)**, keyed on `platform_request_id = ct-<cloud_task_id>-<seq>-<model>`, grouped by `cloud_task_id`, summed at invoice.

Rationale over a per-task single-row upsert: matches the existing append-and-SUM ledger pattern; clean immutable-insert idempotency on `platform_request_id` (exactly ADR-015); best crash/partial story (each finished invocation is durable before the next starts).

**Schema reconciliation — migration `053` (written; required for D4).** Mig 051 has `UNIQUE(cloud_task_id)`, which forces one row per task and blocks the per-(invocation, model) rows. `053_agent_token_usage_cloud_task_multi.sql` relaxes it (052 was already taken by `payment_connections`):

```sql
DROP INDEX IF EXISTS idx_agent_token_usage_cloud_task;
CREATE INDEX IF NOT EXISTS idx_agent_token_usage_cloud_task
    ON agent_token_usage (cloud_task_id) WHERE cloud_task_id IS NOT NULL;  -- non-unique
```

Forward-only, additive, safe: nothing writes `cloud_task` rows yet, so no data is affected. `platform_request_id` remains the UNIQUE idempotency anchor for both sources. **Written and committed; applies automatically on the next deploy** (the boot-time migration runner discovers `*.sql` by name order). The 051->053 transition was rehearsed on a throwaway Postgres 16: post-053 two rows may share a `cloud_task_id` while `platform_request_id` stays UNIQUE (§11).

> Zero-migration fallback (not recommended): keep `UNIQUE(cloud_task_id)`, make the hub accumulate a running total across invocations and `ON CONFLICT (cloud_task_id) DO UPDATE` with **replace** (not add) semantics using a monotonic total. Idempotent, but introduces a second, mutable write pattern and a worse crash story (the running total lives only in hub memory until the next checkpoint).

## 9. Where the capture hooks into the hub run-loop

Single choke point: the subprocess wrapper that spawns `claude -p`, not each call site.

1. Per cloud-task, keep an in-run monotonic `seq` counter.
2. After each `claude -p` child exits: if exit==0, parse stdout's terminal result JSON (or the terminal `result` event under `stream-json`); iterate `modelUsage` to produce one row per model (`total_cost_usd`/`usage` are the aggregate fallback when `modelUsage` is absent).
3. Build one payload per row with `platform_request_id = ct-<cloud_task_id>-<seq>-<model>`; write each to the local spool; POST to the console ingest (async, backoff).
4. On non-zero exit or unparseable output: attempt salvage (stderr / last stream event); otherwise skip billing for that invocation (§7) and let the status webhook carry the failure.

## 10. Out of scope

- Per-provider-attempt accounting (`provider_attempt_id` populated) — reserved, follows ADR-015 if cross-provider fallback appears in the hub.
- Breaking cache tokens into their own ledger columns — folded into `prompt_tokens` for MVP; a later additive migration can split them if cache economics need separate reporting.
- Real-time/streaming spend display — accounting takes the final result only.

## 11. Verification status

**Implemented + verified in this repo (console side):**

- **Ingest endpoint** `POST /api/v1/webhooks/dadagent/usage` (`webhooks_dadagent_usage.go`), registered in the same conditional block as the status webhook (`router.go`), reusing the identical `agentVerifier` (azp=dada-agent) gate — no new client or scope.
- **Tenancy is resolved console-side, never trusted from the hub:** the correlation id (`intent_id`, else `cloud_task_id`) resolves the `cloud_tasks` row -> canonical `id` + `project_id` + `environment_id`; `org_id` via `projectOrg`. The provider USD `cost_usd` IS trusted verbatim (Claude Code's own `modelUsage[model].costUSD`).
- **Idempotent upsert** `ON CONFLICT (platform_request_id) WHERE platform_request_id IS NOT NULL DO NOTHING`, `source='cloud_task'`. Empty-usage callback (`total_tokens<=0 && cost_usd<=0`) is a `200 {stored:false}` no-op; unknown correlation id -> `404`.
- **Migration 053 rehearsed on real Postgres 16:** 051 UNIQUE -> 053 drops+recreates non-unique; post-053 two rows share `cloud_task_id` (`INSERT 0 2`) while `platform_request_id` stays UNIQUE (duplicate -> `ERROR duplicate key`). The exact handler upsert sent twice -> `INSERT 0 1` then `INSERT 0 0`, one ledger row (retry/crash-replay safe).
- **Tests + gates green:** unit tests cover every auth-gate + validation branch (`webhooks_dadagent_usage_test.go`); `go build ./...`, `go vet`, and `TestOpenAPICoverage` pass. The route escapes the OpenAPI coverage gate exactly as the sibling status webhook does (both register only when `KEYCLOAK_ISSUER` is set, which the test cfg leaves empty), so no swagger annotation is required.

**Closed by prior investigation:**

- **`claude -p --output-format json` field shape** — captured from a real run (Claude Code 2.1.215): `total_cost_usd`, `usage{input/output/cache_read/cache_creation}`, and the `modelUsage` per-model map with no top-level `model` field (§4). Residual is version-only (hub item 1 below).
- **Auth gate `[code]`.** The status webhook already gates on `azp=dada-agent` (`webhooks_dadagent.go:93`); the console's own outbound identity toward the hub is a different SA (`dada-cloud-backend`, `CLOUD_AGENT_CLIENT_ID`), so there is no collision.

**Implemented + verified in the hub repo (`agent_sync_hub` @ `058d608`):**

- **Capture** (`app/workspace/claude_report_helpers.py`, `types.py`): `_usage_to_tokens` now also reads `cache_creation_input_tokens` onto `TokenUsage.cache_creation_tokens`; `_extract_model` resolves the concrete model id (result `modelUsage` first key -> last assistant `message.model` -> system-init `model`) onto `ClaudeRunReport.model`. This closes the two shape residuals (cache-creation fold + model source) the hub parser already handles `stream-json` for — reuse, not a new parser.
- **Transport** (`app/backend/agentsync/callback_client.py`): `CallbackClient.send_usage(event)` POSTs to `callback_url.rstrip('/') + '/usage'`, reusing the exact Keycloak client-credentials bearer + retry/backoff of the status `send()`. With `CLOUD_TASK_CALLBACK_URL = https://console.dada-tuda.ru/api/v1/webhooks/dadagent`, the usage target resolves to `.../webhooks/dadagent/usage` — the console route registered in the same conditional block. Same egress path the status webhook already uses (hub-egress item resolved).
- **Emit** (`app/backend/agentsync/cloud_task_runner.py`): after a successful claude run the runner emits **one** metered record via `build_usage_payload(...)` -> `send_usage(...)`. `platform_request_id = ct-<correlation>-<run_id|0>-<model>` where `correlation = cloud_task_id` (else `intent_id`) — `run_id` disambiguates genuine re-dispatches while a retried delivery of the same run collapses on the console's `ON CONFLICT`. `prompt_tokens = input + cache_read + cache_creation`; `cost_usd` = the run's authoritative `total_cost_usd` passed through verbatim. The **codex** path emits nothing (no authoritative cost/model). `cloud_task_id` is in-process at this point for both the intent and runs/autofix paths (the hub mints and owns it), confirming spec point-2 attribution hub-side.
- **Deliberate simplification vs §8:** one record per invocation (aggregate cost + representative model), not one per `modelUsage` entry. Cost integrity holds — `total_cost_usd` is the whole-run figure, so no double-count and no miss; per-model split is a later additive refinement if needed. `seq` from §9 is realized as `run_id` (globally unique per dispatch, crash-replay stable) rather than an in-run monotonic counter.
- **Hub tests green:** `test_claude_report_helpers.py` (cache-creation + model precedence/fallback), `test_callback_client.py` (`send_usage` hits `.../usage`, trailing-slash tolerant), `test_cloud_task_runner.py` (full run drives one usage POST with the exact folded numbers; codex run stays silent; `build_usage_payload` field-aligns with the console `dadaAgentUsageCallback` struct and is None when nothing to bill).

**Proven on live prod (2026-07-25):**

- Console route `POST /api/v1/webhooks/dadagent/usage` is deployed + gated (unauth -> `401`, byte-identical to the sibling status webhook).
- Migration **053** is applied on the prod `cloud-console` DB: `idx_agent_token_usage_cloud_task` is now **non-unique** (`indisunique=false`), `platform_request_id` stays the sole UNIQUE idempotency anchor.
- **Live auth + tenancy wire smoked end-to-end** from inside the hub pod: minted a real `dada-agent` client-credentials token against prod Keycloak, POSTed a hub-shaped payload to the live console route with `intent_id` of a real `cloud_tasks` row and an unbillable body (`total_tokens=0, cost_usd=0`). Result `200 {"ok":true,"stored":false}` — token **accepted** by the console verifier (not `401`), `azp=dada-agent` gate passed (not `403`), payload **bound cleanly** to the Go `dadaAgentUsageCallback` struct (not `400`), `resolveCloudTaskTenancy` **resolved the real row** (not `404`), and the empty-usage branch no-op'd. Ledger row count stayed `0` after the probe — zero billing pollution. The only path element not exercised live is the final `recordCloudTaskTokenUsage` INSERT, which is separately rehearsed on real PG16 (exact handler upsert twice -> `INSERT 0 1` then `INSERT 0 0`).

**Residual (needs the deploy cycle + a real run, not code):**

1. Redeploy the hub with `058d608` (the running backend predates it), then fire one real claude cloud-task and confirm a `source='cloud_task'` row lands in `agent_token_usage` with the resolved tenancy — i.e. exercise the one remaining segment (hub emit -> INSERT) that the live smoke above deliberately left dry to avoid writing a fake billing row.
2. Confirm the **pinned** Claude Code version in the hub workspace image emits the same `stream-json` shape as 2.1.215 (parser fallbacks already tolerate `modelUsage` absent + top-level `model`).
