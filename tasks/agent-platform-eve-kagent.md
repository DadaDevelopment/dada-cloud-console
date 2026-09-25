# Agent platform: eve-kagent + eve parity backlog

Started 2026-09-25 from the eve.dev vs Dada agent runtime comparison.
Source of the comparison: eve.dev docs (llms-full.txt, v0.66.3) and a code map of
tg-gateway -> agent-runtime -> kagent -> MCP (tg-agent-tools), plus the console panel.

Direction chosen by owner: **eve-kagent** = eve.dev framework as the agent engine,
made k8s-native by Dada adapters (workflow world on our PG, sandbox on Boxes/Substrate,
Keycloak auth, ai-gateway model provider, Langfuse OTel, Dada Telegram channel),
deployed as a Dada app (one pod per project workspace, not one pod per agent).
Not a kagent fork, not an own agent loop.

Rule: items in A-C are independent of the engine decision and must not be dropped
if eve-kagent slips.

## Economics baseline [live, kubectl top + requests, 2026-09-25]

| Unit | request RAM | used | CPU idle |
|---|---|---|---|
| kagent agent pod (python ADK), x4 | 384Mi (limit 1Gi) | 208-281Mi | 3-5m |
| MCP tool pod, x4 | 128-256Mi | 51-120Mi | 4-8m |
| agent-runtime (Go, all conversations) | 64Mi | 11Mi | 1m |
| tg-gateway (Go, all bots) | - | 8Mi | 10m |
| kagent controller x2 + ui | 448Mi | 182Mi | - |

Marginal agent+MCP = ~0.5-0.64 GiB reserved. 4 agents + 4 MCP = ~2.2 GiB requested.

## A. Security / ops (independent)

- [x] By-name agent routes gated on owning project role: `9b169b77` (main). Prod delivery not verified.
- [x] Agent names global, saveAgent refuses a name another project holds: `37a2b4cc` (main).
- [x] tg-agent-tools `/mcp` bearer gate: tg-agent-tools `dcdb0c3` (memory: project_tg_agent_tools_mcp_bearer_gate.md).
- [x] tg-gateway `POST /outbound` has no auth (ClusterIP only): add token or NetworkPolicy. Done 2682766a: bearer `TG_GATEWAY_TOKEN` on /bindings + /outbound (503 when unset), console + agent-runtime send it; argo-infra d7e13c48e (token in tgGateway.secret, chart pin). NetworkPolicy skipped: argocd-prod has none in git, live cluster unreachable to check a first NP.
- [ ] Hand-applied ModelConfigs `tg-referral-glm-53-flash`, `tg-vibecoder-glm-53` live outside argo-infra: move into git.
- [ ] z.ai calls (agents + judge) bypass ai-gateway ledger `agent_token_usage`: route via ai-gateway. Prereq for C.6.
- [ ] Orphan RemoteMCPServer `kagent/tg-agent-tools` (401 every minute, no Agent uses it): trace readers, then delete.
- [ ] `ValidateAgent` does not check ModelConfig existence or tool ownership.
- [ ] MCP `saveAgent` rejects a tools-only save although its doc says omitted fields are kept.
- [x] tg-gateway runtime failure no longer silent: binding setting `tg_bindings.on_failure` (`notice` default / `silent` opt-in) + `failure_notice`, `PUT /bindings/{agent}/failure`: `cb9e271c`. Measured before: 19/1058 tg-exchange-support conversations (09-15..09-25) ended enabled with the last user message unanswered. Prod delivery not verified.
- [x] tg-vibecoder stays on direct A2A; ask_user resume ported to `tggateway/a2a.go`: `b2ecbf09`. Not moved behind runtime: runtime flags (judge, question budget, funnel order, split, handoff) are process-wide exchange-support tuning, needs C.2 per-agent config first; then direct path can go.
- [x] Doc drift fixed (runtime->A2A fallback removed from both docs): `cb9e271c`.
- [~] Plaintext secrets in claims/values (Langfuse keys, tool bearer): owner rule says creds in git are the norm; only fix where rotation pain demands (composition `headersFrom` Secret ref).

## B. ddc CLI (independent) [origin: DadaDevelopment/ddc cli-v0.3.1]

- [x] Local `~/.local/bin/ddc` is the Aug build (login/deploy only): reinstall via install.sh. Done 2026-09-25: install.sh -> cli-v0.4.0 (tag on 0959ad5), release workflow's install.sh check green.
- [x] `ddc agent eval` with no `--suite` runs only `markers` (runner default) while help says "every suite". ddc 378ad81: iterates evals/suites/*.yaml, per-suite `--output-dir <base>/<suite>`, worst exit code wins.
- [x] `eval_test.go` example passes `--environment`, unknown to `eval_run.py` (argparse exit 2). ddc 378ad81: `--label`/`--transport`.
- [x] README Langfuse naming contract is stale (evals are report-only now). ddc 0959ad5: report.json/summary.md/history.jsonl section + exit codes.
- [x] `DDC_CLIENT_ID_SECRET` accepted by `MachineCredentialsPresent()` but never read. ddc 9553781: dropped.
- [x] Config errors flattened to exit 1; eve contract is 0 pass / 1 fail / 2 config error. ddc 378ad81 (eval, runner exit 2 propagated) + a49a1d1 (unknown agent action/flag -> 2).
- [~] No command to message/tail a deployed agent (`ddc agent invoke --url`, `ddc agent logs --remote`). ddc a49a1d1: `ddc agent invoke --text ... [--agent --project --env]` via POST /api/v1/agents/{name}/message. Open: `logs --remote`; invoke not yet run against a live agent.

## C. eve parity features (delivered by eve-kagent if spike is green, else built on our engine)

1. [ ] HITL protocol: `input.requested` / `inputResponses`, turn parks durably, per-tool approval `never|once|always|auto`; console HITL inbox.
2. [ ] Channels as config: `turnPolicy steer|queue`, debounce, pacing, reply split, group triggers, output guards all set per channel/agent config, no tg-exchange-support hardcode in the platform.
3. [ ] OpenAPI connections (+ MCP) with allow/block, approval, brokered creds; GTR `http_call_v1` becomes an OpenAPI connection, no pod needed.
4. [ ] One history: single source of truth for the conversation across all hops.
5. [ ] Durable sessions: step checkpoints, replay, park without compute, survive pod restart mid-turn.
6. [ ] Per-session token/cost limits with pause-and-approve (needs A: z.ai via ai-gateway).
7. [ ] Eval DSL eve-compatible in ddc: trajectory assertions (calledTool, notCalledTool, toolOrder, usedNoTools, maxToolCalls, noFailedActions), outputEquals, judge choice, `--tag/--exclude-tag`, `--junit`, `--strict`, `--list`; YAML suites keep working (eve `loadYaml` fan-out).
8. [ ] Dev loop: `ddc agent dev` (local, hot reload), `ddc agent invoke` headless, eval against deployed URL.
9. [ ] Deploy/versioning: idle sessions hand off to new version, in-flight pinned, rollback.
10. [ ] Density: pod per project workspace, MCP tool pods replaced by in-process connections where possible.
11. [ ] Identity: signed forwarded principal + explicit trusted forwarders instead of raw `x-dada-*` headers.
12. [ ] Panel (console assistant) on the same engine: 3 runtimes -> 1.

## D. eve-kagent spike (throwaway, answer = numbers)

Workspace: session scratchpad, not the repo.

- [ ] D1 RSS/latency: eve workspace with 3-5 agents, idle and 10 concurrent sessions.
- [ ] D2 Pre-send guard with one bounded rewrite without forking eve (channel delivery or model middleware).
- [ ] D3 Postgres workflow world: exists, and a `kill -9` mid-turn resumes.
- [ ] D4 One tg-agent-tools YAML suite runs through `eve eval` via a loadYaml adapter.
- [ ] Verdict written here with numbers.

## Review

(filled after spike)
