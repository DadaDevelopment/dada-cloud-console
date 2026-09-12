# Plan: Composio (managed OAuth + per-user tool sessions) in the DADA agent platform

Source: owner's message with the Composio guide set (sessions, authentication,
managed-vs-custom auth, manual auth, connected accounts, sessions-via-MCP, meta
tools). Owner's own sequencing: "я бы начал с Hermes integration -> прямо сейчас
подключил MCP и проверил Gmail/GitHub, а после этого уже читал Sessions/Auth с
мыслью о встраивании в платформу."

Status: plan only. Nothing is built. No Composio account exists yet, no key, no
measurement. Every number below that is not tagged `[live]` is a design claim.

## 1. What Composio actually gives us (grounded)

`[origin docs]` A **session** = `composio.create(user_id)`: scopes one user's
connected accounts, the toolkit/tool allowlist, the auth configs, and execution
state. Connections persist under `user_id` and are reused by later sessions.
`user_id` must be a stable DB id.

`[origin docs]` **Managed auth**: Composio owns the OAuth app, stores and rotates
tokens (~30 min rotation), emits `composio.connected_account.expired`. Credentials
never touch our app or the model. Custom auth configs (`auth_configs={"github":
"ac_..."}`) swap in our own OAuth client per toolkit; mixing per toolkit is
supported, so "DADA Cloud wants access to your Google account" for Google/GitHub/
Slack and managed auth for the long tail.

`[origin docs]` **Manual auth**: `manage_connections=False` kills in-chat auth;
`session.authorize(toolkit, callback_url=...)` returns a Connect Link
`redirect_url`, and the callback receives `status` + `connected_account_id`.
`session.toolkits()` reports per-toolkit connection status. That is exactly the
shape of a console "Integrations" page.

`[origin docs]` **Sessions via MCP**: `mcp=True` -> `session.mcp.url` +
`session.mcp.headers`, a **per-user MCP endpoint**. With
`session_preset=DIRECT_TOOLS` the URL serves exactly the listed tools, no meta
tools. Trade-off spelled out by the docs: over MCP the client talks to Composio
directly, so **SDK `beforeExecute`/`afterExecute` modifiers and session-bound
custom tools do not run**. Anything we need around a call (audit, write-confirm,
quota, redaction) has to be OUR code in front of the URL, not a Composio hook.

`[live 2026-09-12, composio.dev/pricing]` Money shape: tool calls $0.0003 each
(100K/mo free on Hobby), Composio-managed OAuth apps add $0.0002/call beyond 20K
free calls/mo, trigger events $0.003 each (50K free), connected accounts
unlimited and free, sandbox LLM tokens $3.75/M (only if Composio runs the model).
Log retention 7d Hobby / 30d Pro. **ZDR, IP allowlist, BAA, advanced
white-labeling are Pro add-ons billed per call**, Enterprise for custom terms.
Meta tools (tool search) are free — so the agentic discovery loop is cheap, the
executions are what bills.

## 2. Where it can and cannot plug into what we already have

Two planes, and they are not the same problem.

### Plane A — our own operator-side agents (Hermes, Claude Code, console agent chat)

Single human, single set of accounts, no multi-tenancy. Composio's native agent
plugin / MCP URL is a drop-in. Zero platform code.

### Plane B — tenant agents on the kagent runtime (the product)

`[code argo-infra .../crds/managedagent-xrd.yaml:132-200]` A ManagedAgent's
`tools[]` entry renders a **RemoteMCPServer CR** with a static `url`, static
`headers`, and `allowedHeaders`. `[code gitops-agent/internal/renderer/managedagent.go:106-122]`
That is a git-rendered manifest, one object per name in one shared runtime
namespace.

Consequences, in order of importance:

1. **A per-end-user MCP URL cannot live in git.** `session.mcp.url` is minted per
   user at runtime; a CR per user in a shared namespace is not a design, it is a
   leak and a name fight. `[code backend/internal/api/agents.go:499-553]` the
   console already refuses name takeovers for exactly this reason.
2. **The identity channel exists but is empty.** `allowedHeaders` is the whole
   multi-tenant story (XRD comment: caller identity travels in A2A request
   headers, replayed onto MCP calls; the controller proxy drops custom headers, so
   callers must hit the agent's own Service). Live examples set it
   (`x-reels-telegram-id`, `x-telemost-chat-id`).
   `[code backend/internal/tggateway/a2a.go:85-102]` and
   `[code backend/internal/agentruntime/a2a.go:83]` — **our own harness sends only
   `Content-Type`**. So today the platform's own gateway cannot tell a tool server
   which end user is talking. This is prerequisite work, not Composio work.
3. **We already own every other piece the integration needs**:
   - `[code backend/internal/agentruntime/store.go + migrations/148_conversation_state.sql]`
     `conversations(agent_name, channel, external_id, actor_external_id)` is the
     stable end-user identity -> the natural `user_id` source.
   - `[code backend/migrations/145_tg_bindings.sql]` the precedent for a live
     third-party secret: one table, one owning service, console proxies through
     that service's internal HTTP API, never touches git.
   - `[code backend/internal/crypto/crypto.go]` AES-256-GCM at rest with
     `GITOPS_ENCRYPTION_KEY`.
   - `[code mcp-server/internal/reflect/proxy.go + internal/auth/bearer.go]` a
     working MCP proxy with bearer passthrough — the shape of the broker below.
   - `[code backend/migrations/051..147 agent_token_usage*]` a usage ledger to
     hang Composio cost on.

## 3. Architecture: one broker, not one CR per user

```
end user (telegram/web)
   -> tg-gateway / agent-runtime   (adds identity headers)
      -> kagent Agent              (allowedHeaders replay)
         -> RemoteMCPServer "composio-<project>"   [one static CR per project]
            -> composio-broker (ours, in-cluster)
               resolve header -> conversations row -> user_id
               session cache  -> session.mcp.url + headers
               policy / quota / audit / cost
               -> Composio hosted MCP (per-user session)
```

Why a broker and not the session URL in the CR:

- keeps git static and the name space clean (one CR per project, guard already
  written);
- restores everything MCP transport takes away (the docs' own trade-off list):
  audit rows, the write-confirm gate the agent chat already has, per-project
  toolkit allowlist, quota, redaction of upstream error bodies
  (`sanitizeModelReply` precedent);
- makes Composio swappable: the agent sees `composio-<project>`, whatever is
  behind it;
- gives one place to bill tool calls into `agent_token_usage`-style rows.

`user_id` format: `dada:<project_id>:<channel>:<external_id>` — stable, derived
from a DB primary key, never an email (docs' own rule).

## 4. Phases

### Phase 0 — spike, sandbox only (target: same day)

Owner's sequencing. Deliverables are measurements, not code:

1. Composio account + API key; key in `/opt/data/secrets/` for the spike (NOT in
   git, NOT in the repo).
2. Connect Gmail + GitHub over MCP from this box's Hermes; run 3 real tasks.
3. Record: tool calls per task, wall-clock latency per call, what the Connect
   Link consent screen says ("Composio wants access..."), whether SSE or
   streamable HTTP, header shape.

Exit criterion: a real transcript with real numbers. If latency per call or cost
per task is unacceptable here, the rest of the plan does not start.

### Phase 1 — plane A rollout (no platform code)

Composio MCP/plugin for the operator-side agents (Hermes, Claude Code). This is
the cheapest real usage and it generates the cost/latency data Phase 3 needs.

### Phase 1.5 — quick win on plane B: single-account agents

An agent that acts as ONE company account (support Gmail, one GitHub bot, one
Slack workspace) needs **no broker and no identity plumbing**: create one session
with `DIRECT_TOOLS` + `mcp=True`, put that URL in the agent's `tools[]` entry as
a normal RemoteMCPServer with the session headers as static `headers`, token in
the agent's env as `${COMPOSIO_...}` (the `${VAR}` mechanism already exists).
Ship this first — it is a day of work and it is the shape most tenant agents
actually want. Explicit limit to write in the UI: every end user shares that one
account.

### Phase 2 — identity plumbing (prerequisite for per-user)

Real Go work in this repo, independent of Composio and valuable on its own:

- `A2AClient.SendWithContext` gains identity headers (conversation id, project,
  channel, external id) — a new method, existing callers unchanged (the
  `SendWithContext` precedent).
- agent-runtime does the same on its A2A path.
- ManagedAgent claims get the matching `allowedHeaders` from the console side.
- Verification bar: a header echo through a real kagent Agent to a real MCP
  server, asserted on the received header, not on a 200.

### Phase 3 — composio-broker

New component: `backend/cmd/composio-broker` + `backend/internal/composio`.

- `composio_sessions(user_key, session_id, project_id, created_at, last_used_at)`
  — sessions persist server-side forever per docs, so we must own the mapping and
  its cleanup.
- `composio_connections` mirror for the console UI (id, toolkit, status,
  connected_account_id, updated_at), fed by `session.toolkits()` and by the
  `connected_account.expired` webhook.
- MCP JSON-RPC passthrough with per-project toolkit allowlist; **fail closed**
  when the identity header is missing or unresolvable (serve zero tools, say so —
  never fall back to a shared account).
- audit row + cost row per tool call.
- API key: one platform key in a k8s Secret, delivered like `TG_GATEWAY_DB_URL`
  (`kubectl patch secret`), never in values, never in git.

### Phase 4 — console UX (Integrations)

`manage_connections=False`. Per-project page listing toolkits with
Connect/Connected, `session.authorize(toolkit, callback_url=<console>/callback)`
on click, callback route reading `status`/`connected_account_id`, multiple
accounts per toolkit supported (work/personal), disconnect, and an
`expired` -> notification path (the notify package exists).

### Phase 5 — production auth posture

Own OAuth apps for Google/GitHub/Slack (`auth_configs` per toolkit) so the
consent screen says DADA Cloud; managed auth for the rest. Decide the Pro add-ons
(ZDR, IP allowlist) with the cost model from Phase 1's real numbers.

## 5. Open questions the owner has to answer before Phase 3

1. **Data residency / legal.** Per-user Gmail/Slack tokens for Russian clients
   would live in a US SaaS, and payloads are retained (7d/30d) unless ZDR is
   bought. This is the real blocker, not the code. Phase 1.5 (single company
   account, our own consent screen) does not have this problem at the same scale.
2. **Who pays.** $0.0003/call + $0.0002 managed-app surcharge is invisible per
   call and loud in an agentic loop. Flat platform cost, or metered into the
   tenant's bill through the usage ledger?
3. **Blast radius.** A tenant agent with a user's real Gmail write scope is a
   different risk class than our current tool servers. Does the write-confirm gate
   from agent chat become mandatory in the broker for write tools?
4. **Scope of catalog.** Full 1500 toolkits discoverable, or a curated per-plan
   allowlist (the console already curates its own MCP surface at ~45-50 tools for
   exactly this reason)?

## 6. What this plan deliberately does not do

- No new project in the cluster (CLAUDE.md hard rule) — everything lands in
  `agent-sandbox` for testing.
- No Composio SDK inside the kagent agent images: the agent only ever sees an MCP
  server, so a vendor swap is a broker change.
- No per-user CR of any kind in git.
- No "platform ready" claim: this file is a plan, and each phase reports with a
  real run (build/vet/test on the container rig, and a live transcript for the
  spike).
