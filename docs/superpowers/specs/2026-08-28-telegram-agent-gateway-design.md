# Telegram ↔ kagent agent gateway

Status: approved, implementing.

## Problem

Console can already create a kagent `ManagedAgent` from a prompt
(`backend/internal/api/agents.go`, `gitops-agent/internal/worker/managed_agent.go`).
There is one hand-wired example of an agent behind a Telegram bot
(`telemost-bot`, via `AGENT_A2A_URL` env pointing at
`http://<agent>.kagent.svc.cluster.local:8080`), but every new bot today means
a bespoke app, a bespoke values.yaml, a human doing the wiring.

Goal: any agent created in the console can be bound to a Telegram bot token
from the same create/edit form. One bot ↔ one agent. No new page, no new
top-level product surface — a field on the agent that already exists.

## Non-goals

- Multiple bots per agent or multiple agents per bot.
- Webhook transport (rejected: needs public DNS+TLS per bot; long-polling in
  one shared process was the explicit ask).
- Editing a bound token in place (disconnect, then reconnect).
- Migrating `telemost-bot` onto this path.

## Architecture

```
console UI (agents modal)
   │ POST /api/v1/agents/{name}/telegram   {bot_token}
   │ DELETE /api/v1/agents/{name}/telegram
   │ GET /api/v1/agents/{name}/telegram
   ▼
backend (Handler, cluster-internal proxy — same shape as kagent.Reader)
   │ HTTP, ClusterIP-only
   ▼
tg-gateway (new binary, backend/cmd/tg-gateway, own container+Deployment)
   │ owns Postgres table tg_bindings (own migration, own DB role)
   │ replicas: 1, strategy: Recreate  (long-poll: 2 pollers on one token = 409)
   │
   ├─ reconcile loop (~5s): tg_bindings rows ⇄ live goroutines
   ├─ per-binding goroutine: Telegram getUpdates long-poll
   │       → POST http://<agent_name>.kagent.svc.cluster.local:8080 (A2A)
   │       → Telegram sendMessage with the reply
   └─ bind: validates token via Telegram getMe before persisting
```

`tg-gateway` derives the agent's A2A URL from `agent_name` (deterministic,
same convention `telemost-bot` uses by hand) — the bind payload only needs
`{agent_name, project_id, bot_token}`.

### Why not go through git/Argo like the agent itself

The agent's prompt/tools are infrastructure-as-code on purpose (audit trail,
Argo as source of truth). A bot token is a live secret with no config-drift
story to worry about — it is simplest as one Postgres row the gateway owns
and nobody else reads. Putting it in a values.yaml would mean a second
git-committed plaintext secret per bot and a multi-minute sync before the
"paste token, it works" moment — the opposite of the point.

### Why one shared process instead of per-bot pods

Explicit requirement: single replica, N bots multiplexed as goroutines in one
process. Matches the existing `telemost-bot` constraint (`recreate: true`,
"two pods => getUpdates 409 Conflict") generalized to N tokens instead of 1.

## API contract

Backend (`/api/v1/agents/{name}/telegram`, existing auth middleware):
- `POST {bot_token}` → `200 {bot_username}` / `400 {"error": "invalid bot token"}` / `503` gateway unreachable
- `DELETE` → `200 {}`
- `GET` → `200 {bound: bool, bot_username?: string}`

`tg-gateway` internal API (ClusterIP only, no auth — network-policy trust like kagent.Reader):
- `POST /bindings {agent_name, project_id, bot_token}` → validates via `getMe`, upserts row, starts poller, returns `{bot_username}`
- `DELETE /bindings/{agent_name}` → removes row, stops poller
- `GET /bindings/{agent_name}` → `{bound, bot_username}` or 404

`DeleteAgent` (backend) also calls `DELETE /bindings/{name}` on the gateway
when an agent with a binding is deleted, best-effort (log on failure, do not
block the agent delete — an orphaned poller against a dead A2A URL is a
cleanup item, not an outage).

## Frontend

`frontend/app/(console)/projects/[projectId]/agents/page.tsx`, same
create/edit modal, new section below Tools:

- not bound: password input + "Подключить" button
- bound: `Connected as @botname` + "Отключить" button
- changing the token = disconnect, then reconnect (no partial-update path)

Agent card in the grid gets a third status line next to `state.ready` /
`traces_url`: `Telegram: @botname ↗` (links to `t.me/<username>`) when bound.

`lib/api.ts` gets `agentsApi.telegram.{bind,unbind,get}`.

## Infra

Follows the existing `gateway` (telemetry ingest, ADR-012) pattern exactly,
not a new top-level module:
- new binary `backend/cmd/tg-gateway`, new package `backend/internal/tggateway`
- new `backend/Dockerfile.tg-gateway`
- new Jenkinsfile image entry + build stage (mirror `GATEWAY_IMAGE`)
- new Helm templates in `helm/dada-cloud-console/templates/`:
  `tg-gateway-deployment.yaml`, `tg-gateway-service.yaml`, `tg-gateway-secret.yaml`
  (`replicas: 1`, `strategy: Recreate`, mirroring `gitops-agent-deployment.yaml`)
- own migration for `tg_bindings` (backend's migration runner, gateway gets a
  narrower DB role — same split as the telemetry gateway)

## Error handling

- Agent not yet Ready when a message arrives (git→Argo sync still running):
  A2A call fails, gateway retries with backoff; after ~30s of failures sends
  one "агент ещё разворачивается" message to the chat rather than silence.
- Bad token on bind: rejected synchronously via `getMe`, surfaced as a field
  error in the modal — not a silent dead poller.
- Gateway pod restart: reconcile loop rebuilds every poller from `tg_bindings`
  on boot — no manual re-bind needed.

## Testing

- Backend: unit tests for the proxy handler (bind/unbind/get, gateway-down
  503), same shape as `agents_runtime_test.go`.
- `tg-gateway`: unit tests for the reconcile loop (row added/removed → poller
  started/stopped) against `httptest` fakes for Telegram + A2A. No real
  long-poll in CI.
- Manual: one real bot in `agent-sandbox`, live token → prompt → Telegram
  reply round trip before calling this done.
