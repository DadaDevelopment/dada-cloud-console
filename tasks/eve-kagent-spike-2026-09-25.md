# eve-kagent spike, 2026-09-25

Throwaway spike: eve 0.66.3 (Apache-2.0, beta) as the Dada agent engine, self-hosted.
Setup: node 24.21.0, ai 7.0.114, Nitro 3.0.260903-beta. Memory numbers from Linux
(`node:24` container, aarch64, 8 vCPU) unless marked mac. All model calls are eve
`mockModel()`: latencies are framework overhead only, 0 real model calls.
Spike code lived in the session scratchpad (`eve-spike/ws/agents/{g1,d3,d3l,ex,mt}`, `bench.mjs`, `d3-run.sh`) and is not kept.

Overall: **YELLOW. Bounded pilot, not a platform swap yet.**

## D1 Density: YELLOW

The documented workspace (`eve init ws --agents a1,a2,a3`) does NOT serve N agents from one
process self-hosted: root `eve build` emits a Vercel config with one service per member, so
self-hosted = one Node process per agent.

| Topology (Linux, `node .output/server/index.mjs`) | Procs | Idle RSS | Peak after 51 turns | 10 concurrent p50/p95 | Sequential p50/p95 | Cold start |
|---|---|---|---|---|---|---|
| 1 agent | 1 | 166 MB | 355 MB | 846 / 1012 ms | 98 / 140 ms | 708 ms |
| 3 agents (workspace) | 3 | 638 MB (213/agent) | 873 MB | 315 / 517 ms | 95 / 150 ms | 524-1316 ms |
| 5 agents (workspace) | 5 | 988 MB (198/agent) | 1371 MB | 285 / 767 ms | 119 / 615 ms | 774-1270 ms |
| 5 personas in 1 process (`defineDynamic`) | 1 | 167 MB | 368 MB | 358 / 469 ms | 61 / 84 ms | 513 ms |

- Documented layout costs ~200 MB idle per agent: same class as today's kagent python pod (208-281Mi used).
- One process for N agents works via `defineDynamic`: custom AuthFn maps header `x-dada-agent` to a session attribute; dynamic instructions and tools resolve at `session.started` (observed `persona=support tools=ping_support`, `persona=sales tools=ping_sales`, 401 without header). Our convention, not eve's documented path.
- `eve start` wraps and spawns the server: memory doubles (mac: 1575 MB vs 615 MB direct). Run the server entry directly in prod.
- Parked sessions retain ~1 MB each and are not released: 1 process, 400 sessions, 167 -> 575 MB; p50/p95 882/1660 ms; ~10.5 turns/s. Local store ~270 KB disk per one-turn session.
- node_modules 133 MB (168 MB with PG world), eve itself 42 MB; `.output` 8.9 MB per agent, self-contained (mac build ran unchanged on Linux).

## D2 Pre-send guard: GREEN

AI SDK `wrapLanguageModel` middleware around the agent model (~70 lines, no fork): buffers each
step, passes tool-call steps through, checks final text (em-dash, non-allowlisted link), on
violation calls the model once more with draft + violation list, hands only the rewrite to eve.

```
[guard] rewrite violations: em-dash U+2014; link to non-allowlisted host evil.example.com
before: "Sure <U+2014> your deploy is live, see https://evil.example.com/deploy"
after:  "Fixed: your deploy is live - details at https://dada.cloud/docs/deploy"  stillBad: []
grep evil / U+2014 in the /eve/v1 stream: 0 / 0
```

Limits:
- No token streaming of the final answer (held until check/rewrite). Fine for Telegram.
- eve usage records only the rewrite call; a violation costs 2 calls, so usage totals and `maxTokenCostUsdPerSession` under-count. Log draft usage ourselves.
- Draft never reaches stream or durable history. Crash mid-step reruns the whole step (both calls).
- Middleware also wraps compaction calls: set a separate compaction model or a skip marker.
- Policy for "rewrite still bad" is ours (demo only logs `stillBad`).
- Hooks are observe-only; a custom channel can withhold delivery but the draft is already in history and on the stream. Middleware is the right layer.

## D3 Durability on Postgres: YELLOW

`@workflow/world-postgres@5.0.0-beta.46` exists (Apache-2.0). Must be pinned (npm `latest` is 4.3.7) to match eve's internal workflow version. README calls it a reference implementation; its queue endpoint has no auth; native dep `cbor-extract` means build OS/arch must match prod. Config: `experimental.workflow.world`, `WORKFLOW_POSTGRES_URL`, `bootstrap` for schema.

Test: slow tool (10 s sleep), `kill -9` after 4 s, restart 2 s later:

```
start crash-me pid=61579 15:01:34        <- killed mid-tool
restart +7s; after 45s: no progress. graphile_worker.jobs locked_by dead worker
select graphile_worker.force_unlock_workers(array[<dead worker ids>])
start crash-me pid=61770 15:02:27        <- tool RE-RAN from scratch
end   crash-me pid=61770 15:02:37
stream: start events re-emitted with new ids, then action.result -> turn.completed; follow-up turn ok
```

- No self-resume after hard kill: dead worker keeps the job lock; graphile_worker treats locks as stale only after ~4 h. After manual unlock it resumed in ~11 s and completed.
- Tools are at-least-once: side-effecting tools need idempotency keys. Consumers must dedupe start events per turn.
- Default local world is worse: stuck >4 min across 2 restarts; a follow-up message was accepted but never ran.
- NOT tested: SIGTERM graceful shutdown (our normal roll path, Kyverno 180 s grace). OOMKill = kill -9.

## D4 Eval compat: GREEN

`agents/ex/evals/markers.eval.ts` loads the read-only `tg-agent-tools/agents/tg-exchange-support/evals/suites/markers.yaml` via `loadYaml()` (~30 lines adapter), one eval per scenario, matching `eval_run.py` semantics: lowercased last reply, `any_of`/`also_any_of` -> `includes(/a|b|c/i)`, `none_of` + suite `leak_markers` -> none-appear, YAML `tags` -> eve tags.

```
✓ markers/0000..0004  ✓ trajectory gates 7/7
✗ markers/0005 [uk_not_denied_none_of] (fixture answer, expected)
✗ trajectory-negative toolOrder(list_required_documents -> geo_check) (negative control, expected)
Results: 6 passed, 2 failed (8 total)   exit=1   849 ms
```

- Trajectory: `usedNoTools`, `calledTool(name,{input:{q:/nigeria/i},count:1})`, `toolOrder`, `notCalledTool`, `maxToolCalls` all work, negative control fails as it should.
- `--junit` valid (8 cases, 2 failures), `--json` full results, exit 0 on filtered green run, exit 2 on unknown `--tag`.
- `eve eval` drives only eve servers, not kagent A2A agents.
- `target_script.yaml` (stages, facts_after, must_include...) not covered: needs its own adapter.

## Self-hosting snags

- Non-Gateway models need `modelContextWindowTokens` or compile fails.
- Scaffold auth `[vercelOidc(), localDev(), placeholderAuth()]` rejects prod traffic: own AuthFn required.
- Root workspace build and agent-to-agent transport assume Vercel.
- `eve build` builds a 617 MB sandbox Docker image whenever Docker exists, even if unused; 9 default tools (bash, read_file...) enabled. Disable both (`defaultTools: false`, no sandbox).
- CLI telemetry on by default: `EVE_TELEMETRY_DISABLED=1`.
- 51 releases in 30 days; PG world must track eve's internal beta exactly.

## Top risks

1. Crash recovery: kill -9 / OOMKill mid-step leaves sessions stuck (~4 h PG world, indefinitely local world) and tools re-run. We own a dead-worker lock sweeper + idempotency, on a reference-implementation store.
2. Beta churn and version lock-in; one-process-many-agents is not the documented path.
3. Memory: ~170-200 MB base per process, ~1 MB retained per parked session, ~10-25 turns/s per process. Needs a long test with real models.

## Incident during the spike

Port 55432 was already a local Homebrew Postgres 14 of another session. The first D3 run created
schemas `workflow`, `workflow_drizzle`, `graphile_worker` in its `postgres` database (which held
nothing else); the spike dropped exactly those and re-ran on Docker PG :55433. Verified after
[live]: none of the three schemas remain, 0 user tables in `postgres`, `console*` databases untouched.
