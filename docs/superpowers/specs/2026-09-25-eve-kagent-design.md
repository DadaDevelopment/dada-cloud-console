# eve-kagent: declarative agent host on eve

Status: design, 2026-09-25. Owner decisions recorded below.
Backlog and history: `tasks/agent-platform-eve-kagent.md`. Spike evidence: `tasks/eve-kagent-spike-2026-09-25.md`.

## 1. Problem

Today a Telegram turn crosses tg-gateway -> agent-runtime -> A2A -> kagent pod (python ADK) -> MCP pod -> back into agent-runtime callbacks. The console assistant (panel) is a third, separate Go ReAct loop.

- Economics [live 2026-09-25]: each kagent agent pod requests 384Mi (uses 208-281Mi), each MCP pod 128-256Mi (uses 51-120Mi). One agent plus its tools reserves ~0.5-0.64 GiB while idle. agent-runtime holds every conversation of every agent in 11Mi.
- Two histories (agent-runtime `conversations` and kagent sessions per contextId), no durable steps, HITL through workarounds (kagent `ask_user` deadlock), Telegram behaviour hardcoded for one customer project.
- Eval runner sees only reply text: no tool-trajectory assertions.

## 2. Goals and non-goals

Goals:
1. Many agents per process: marginal agent cost in MB, not in pods.
2. One conversation history, durable sessions that park without compute and survive restarts.
3. First-class HITL: `input.requested` / `inputResponses`, per-tool approval.
4. Channel behaviour and output guards as agent/channel config, no project-specific hardcode in the platform.
5. OpenAPI and MCP connections executed in-process with brokered credentials.
6. eve-compatible evals and dev loop through `ddc`.
7. Versioned deploy with idle-session handoff and one-field rollback.

Non-goals for this spec (later sub-projects): BYO eve projects with customer TS code (tier 2), moving the console panel onto the host, removing kagent from the cluster, voice/vision channels.

## 3. Decisions

| # | Decision | Why |
|---|---|---|
| D1 | Engine = eve (Apache-2.0) made k8s-native by our adapters. Not a kagent fork, not our own loop. | Owner call. Spike: guard and evals work without forking eve. |
| D2 | Tier 1 "declarative agents": agent = data resolved per session via `defineDynamic` in one host process. | Spike D1: documented eve workspace = 1 process/agent at ~200 MB (no gain over kagent); dynamic host = 5 agents in 167 MB. |
| D3 | One shared host pool for all projects (owner choice). | Max density; no customer code runs in tier 1. Isolation by session scope, per-project credentials and quotas. |
| D4 | tg-gateway stays the Telegram transport; eve owns turn semantics. | Go, 8Mi, proven long-poll per bot token; eve `turnPolicy` handles overlapping messages when tg-gateway posts into an existing session. |
| D5 | Output guards = model middleware calling the existing Go `agentjudge` precheck. | Spike D2: `wrapLanguageModel` keeps the draft out of stream and history. Judge spec stays the single policy source (commits 9151241f, c8c7e838, fb63963a). |
| D6 | Durable store = `@workflow/world-postgres` pinned to eve's internal workflow version, plus our dead-worker lock sweeper. | Spike D3: resumes correctly after unlock; without a sweeper a kill -9 stalls a session ~4 h. |

## 4. Architecture

```
Telegram ─ tg-gateway (Go) ─┐  binding + continuation chat_id -> sessionId, debounce, pacing, split
console / ddc / MCP ────────┤
                            ▼
        ┌──── eve-host pool (Node, N replicas, HPA) ─────────────────────────┐
        │ AuthFn: Keycloak JWT | agent grant | trusted forwarder (tg-gateway) │
        │ defineDynamic @ session.started: load AgentDef(project, agent, ver) │
        │   instructions, skills, connections, model, channel, guards, limits │
        │ model = ai-gateway provider  ─ wrapped by guard middleware ─────────┼─► agentjudge precheck (Go)
        │ connections: MCP / OpenAPI, creds from project Secret per request   │
        │ hooks: judge (async), audit, usage                                  │
        │ OTel -> Langfuse                                                    │
        └──────────────┬──────────────────────────────────────────────────────┘
                       ▼
          world-pg (sessions, steps, events, queue) + lock sweeper
```

Components:

| Component | What | Where |
|---|---|---|
| `eve-host` | eve server with one dynamic agent entry; resolves AgentDef per session; runs server entry directly (not `eve start`, which doubles memory). | new `agent-host/` dir in dada-cloud, own image, Jenkins build |
| `@dada/eve` | adapters: auth, ai-gateway model provider (sets `modelContextWindowTokens`), guard middleware, Langfuse OTel, world-pg config, sweeper, connection credential broker | package inside `agent-host/` until a second consumer exists |
| AgentDef API | console serves resolved, versioned agent definitions to the host | `GET /internal/agents/{project}/{agent}@{version}` in backend, service-token auth |
| `agentjudge` precheck | Go judge exposed over HTTP for the guard middleware; same specs as agent-runtime | backend service route or small binary reusing `internal/agentjudge` |
| tg-gateway | transport: posts to eve session API instead of agent-runtime/A2A for `engine: eve` agents | `internal/tggateway` |
| Console | agents page reads host session snapshots/stream; HITL inbox answers `input.requested` | frontend + backend proxy |
| `ddc` | `agent dev` (local host with one agent), `agent invoke`, `agent eval` via `eve eval` + YAML adapter | DadaDevelopment/ddc |

## 5. Agent definition

Source of truth unchanged: ManagedAgent claim in git plus prompt source (`core.md`, `domains/*.md` -> skills). New claim fields:

```yaml
engine: eve              # kagent (default) | eve; per-agent cutover and rollback
channel:
  turnPolicy: queue      # steer | queue
  debounce: {quietMs: 2500, maxMs: 8000}
  pacing: {baseMs: 20000, maxQuietMs: 90000, cpm: 350}
  split: {seam: "---", maxParts: 4}
  onFailure: notice      # already tg_bindings.on_failure
guards:
  judgeSpec: precheck    # agent's judge spec used before delivery
  rewrite: 1
limits: {maxCostUsdPerSession: 1.5, sessionTimeout: 14d}
connections:
  - {name: crm, type: openapi, spec: https://.../openapi.json, operations: {allow: [...]}, auth: {secretRef: crm-token}}
  - {name: kb, type: mcp, url: http://...svc:8000/mcp, tools: {allow: [...]}, approval: never}
```

AgentDef = claim + prompt source resolved by the console into one JSON with a content hash as `version`. The host caches by `(project, agent, version)`. transport-side keys (`debounce`, `pacing`, `split`, `onFailure`) are read by tg-gateway, host-side keys (`turnPolicy`, `guards`, `limits`, `connections`) by the host.

## 6. Turn data flow

1. tg-gateway receives a burst, debounces, resolves `chat_id -> sessionId` (creates the session on first contact), POSTs the message with a signed forwarded principal `{project_id, agent, channel: telegram, external_id}`.
2. Host AuthFn verifies the forwarder signature, sets `session.auth`. At `session.started` `defineDynamic` loads AgentDef; at `turn.started` it re-checks the version (idle handoff, section 9).
3. Model call through ai-gateway (every call, including guard drafts, lands in `agent_token_usage`).
4. Tool calls: connection broker injects project credentials and `Idempotency-Key: <sessionId>:<turnId>:<toolCallId>`; principal is passed as a signed header, never raw `x-dada-*` from the caller.
5. Final text step: guard middleware calls agentjudge precheck; on violation one rewrite with the judge's ask; still bad -> spec's handoff line + HITL/escalation per spec actions.
6. tg-gateway reads the NDJSON stream, splits and paces delivery; on `input.requested` renders inline buttons; `inputResponses` go back into the same session.
7. Async hook posts judge scores and audit rows.

One history: the eve session. `conversations`/`conversation_messages` are not written for `engine: eve` agents. Console, judge and evals read the host.

## 7. Error handling and recovery

| Failure | Behaviour |
|---|---|
| Pod SIGTERM (roll, scale-in) | Host stops accepting new turns, lets in-flight steps finish within grace (Kyverno 180 s already set for agent pods; set the same on host), releases worker locks on exit. Must be proven by test T3. |
| kill -9 / OOMKill / node loss | Sweeper (runs on every host start and every 30 s by leader) finds graphile_worker locks held by workers not heartbeating for > 60 s and calls `force_unlock_workers`. Target: resume < 60 s. |
| Step re-run after crash | Tools are at-least-once. Connections send `Idempotency-Key`; side-effecting tools without idempotent backends must be marked `approval: once` or wrapped with a dedupe table keyed by the idempotency key. Stream consumers dedupe start events per turn id. |
| Model / gateway error | eve step retry (up to 4 attempts); after that the turn fails, tg-gateway applies `onFailure` (notice by default, never silent). |
| Guard rewrite still violates | Judge spec action decides: handoff line + escalation, or drop for follow-ups. Never deliver the refused draft (same rule as fb63963a). |
| AgentDef API down | Host serves cached versions; new sessions for uncached agents fail with 503 and tg-gateway `onFailure`. |
| Budget exceeded | `limits.maxCostUsdPerSession` pauses the session with `input.requested(session-limit)`; operator approves in console. eve counts only its own usage (draft under-count, spike D2): the ledger is the billing truth, the eve limit is a guard. |

## 8. Quotas and memory (shared pool)

- Per project: max concurrent turns, max active sessions, `sessionTimeout` default 14 d.
- Gate G1 before any second project moves: 10k parked sessions on world-pg in one host. Spike measured ~1 MB retained per parked session on the local world. If memory is not released on world-pg, idle sessions must be evicted from process memory (resume from PG on next message) before the pool is shared.
- HPA on CPU and in-flight turns; stateless routing is fine because state is in world-pg.

## 9. Versioning, deploy, rollback

- Agent change = new AgentDef version (content hash). At `turn.started` an idle session picks up the new version; a session with an in-flight step, pending HITL or active task keeps its version until it goes idle (eve semantics).
- Host image change = normal rolling deploy; sessions resume on new pods from world-pg.
- Rollback: agent -> previous AgentDef version or `engine: kagent`; host -> previous image. Existing eve sessions of an agent rolled back to kagent are not migrated (kagent starts a fresh context; acceptable for pilot, documented in the runbook).

## 10. Testing and pilot

Pilot agent: `tg-exchange-support-eve` in project `agent-sandbox` (clone of the prod bot's prompt, skills, tools, judge specs). Prod bot moves last.

| Test | Pass criterion |
|---|---|
| T1 evals parity | Same suites (`markers`, script suites) via `ddc agent eval` against the host: pass_rate >= current bot on the same model; trajectory assertions added for tool order of the funnel. |
| T2 latency | p95 turn latency <= current path on the same model, 20 scripted conversations. |
| T3 SIGTERM roll | Roll the host mid-conversation x10: 0 stuck sessions, 0 duplicate deliveries. |
| T4 kill -9 | 10 kills mid-tool: every session completes within 60 s of restart, tools with side effects not duplicated downstream. |
| T5 memory G1 | 10k parked sessions: host RSS stays below 1 GiB after eviction. |
| T6 density | 20 agents from 3 projects in one host: idle RSS, isolation test (session of project A cannot reach connections or memory of project B). |
| T7 guard | Seeded violations (em-dash, foreign link, leak markers): 0 reach Telegram; each costs <= 2 ledger rows. |
| T8 HITL | Tool with `approval: once`: parks, answered from console inbox and from Telegram buttons, resumes. |

## 11. Phases

1. P0 prerequisites: ai-gateway has working upstreams (see section 13) and z.ai routed through it (in progress, tracker A).
2. P1 host skeleton: `agent-host/` with dynamic agent, auth, ai-gateway provider, world-pg + sweeper, AgentDef API. Tests T3, T4, T5.
3. P2 guards and judge: precheck HTTP, middleware, async judge hook. Test T7.
4. P3 connections: OpenAPI + MCP broker, idempotency keys; GTR `http_call_v1`/`sql_query_v1` as OpenAPI connection. 
5. P4 channel: tg-gateway `engine: eve` path, continuation map, stream delivery, HITL buttons. Tests T1, T2, T8.
6. P5 ddc: `agent dev` on the host image, `eve eval` + YAML adapter (spike D4), `invoke`, `logs --remote`.
7. P6 cutover: pilot agent green on T1-T8 for 7 days, then tg-vibecoder, then prod bot; reels/telemost after.
Each phase gets its own implementation plan.

## 12. Risks

1. eve beta churn (51 releases in 30 days) and exact workflow-version lock: pin eve and world-pg together, upgrade on a schedule with the eval suite as gate.
2. world-pg is a "reference implementation" with an unauthenticated queue endpoint: keep the host's workflow routes cluster-internal (NetworkPolicy), never on a public ingress.
3. Shared pool blast radius: one bad AgentDef or runaway session affects all tenants. Per-project quotas, AgentDef validation at save time, per-session limits.
4. Parked-session memory (G1) may force eviction work before sharing.
5. Node native dep (`cbor-extract`): build on the same OS/arch as prod.

## 13. Open questions

- ai-gateway upstream outage reported by another session on 2026-09-25: no successful upstream call since ~09-21, egress proxy VM `83.222.23.85` unreachable; console chat, embeddings and reels/telemost agents affected. Not verified in this design session. P0 depends on it.
- Does the host run judge LLM checks through ai-gateway (ledger) or keep a separate judge model budget?
- Where tier 2 (BYO eve project with customer TS tools) routes: same tg-gateway path to an app URL; design later.
