# Composio integrations: one button in the console, per-user tools in the agent

What a client does: opens Integrations in their project, clicks **Подключить**
next to Gmail, signs in on Google's own page, comes back. From that moment their
agent can act in Gmail as them. Composio is never mentioned in the product.

Status 2026-09-12: broker built and verified live on the demo route. Not yet
deployed to the cluster, not yet in the console's Next.js UI.

## Live verification (this session, real Composio project, key ak_KYHK...)

| Step | Signal | Result |
|---|---|---|
| API key valid | `GET /api/v3.1/toolkits` | `[live]` 200, Gmail/GitHub/... catalog |
| Session per user | `POST /api/v3.1/tool_router/session` | `[live]` `trs_8YT8zYAz8jEH`, `mcp.url` returned |
| Session MCP works | `tools/list` on that URL | `[live]` 5 meta tools |
| Broker resolves user | `POST /composio/mcp/<project>` + `x-dada-end-user` | `[live]` proxied, SSE relayed |
| Fail closed, no identity | same without the header | `[live]` JSON-RPC -32001, zero upstream calls |
| Fail closed, unknown user | header for a stranger | `[live]` -32002, zero upstream calls |
| One button | `/composio/demo` -> Подключить (Gmail) | `[live]` real Google consent page "to continue to Composio" |
| Audit | `composio_tool_calls` row | `[live]` tool + agent recorded |

Demo route (token in `/opt/data/secrets/composio-broker-token.txt`):
`https://harness.dada-tuda.ru/composio/demo?project_id=<uuid>&end_user_key=<channel>:<id>&token=<token>`

## The architecture problem, and what had to change

`[code argo-infra .../managedagent-xrd.yaml:132-200]` A ManagedAgent's `tools[]`
entry renders a **RemoteMCPServer CR**: static URL, static headers, one object
per name in **one cluster-global namespace**. `[code backend/internal/api/agents.go:499-553]`
The console already refuses name takeovers because a duplicate name is a fight,
not a merge.

A Composio session's MCP URL is minted **per end user at runtime**. So the naive
mapping -- one CR per user -- is impossible: it is a per-user object in a shared
namespace, unbounded in count, containing a credential-bearing URL, in git.

The platform bends in three places instead:

1. **One static broker URL per project** in the claim
   (`https://<broker>/mcp/<project-id>`). Git stays static. The per-user part is
   resolved at call time.
2. **Identity travels in a request header, not in the manifest.** The mechanism
   already existed and was unused by our own harness: `allowedHeaders` on the
   claim, replayed by the runtime onto MCP calls. `[code backend/internal/tggateway/a2a.go]`
   and `[code backend/internal/agentruntime/a2a.go]` sent only `Content-Type`
   before this change; both now send `x-dada-end-user` + `x-dada-agent`.
3. **The broker is the policy layer the MCP transport removes.** `[origin docs/sessions-via-mcp]`
   Over MCP the client executes against Composio directly, so the SDK's
   before/after-execute modifiers never run. Audit, the fail-closed identity
   check and the toolkit gate exist only because the call passes through us.

```
end user (telegram/web)
  -> tg-gateway / agent-runtime      adds x-dada-end-user
     -> kagent Agent                 allowedHeaders replays it
        -> RemoteMCPServer "composio" (ONE static CR per project)
           -> composio-broker        header -> session -> audit -> proxy
              -> Composio hosted MCP (that user's session, that user's accounts)
```

## Authorization is a server-side allowlist, not a prompt

`composio_sessions.toolkits` mirrors the session's upstream allowlist. Connecting
an app PATCHes the session to add its toolkit; an app the user never connected
has **no tools in the session at all**, so no prompt can talk the agent into
reaching it. Only status `ACTIVE` counts as connected: a connected account exists
from the moment a link is minted, and treating that as permission would put tools
in front of the agent that answer 401.

## Components

| Piece | Path |
|---|---|
| Composio REST client | `backend/internal/composio/client.go` |
| Store (3 tables) | `backend/internal/composio/store.go` |
| Service (user id, connect, sync) | `backend/internal/composio/service.go` |
| MCP proxy | `backend/internal/composio/proxy.go` |
| HTTP surface + demo page | `backend/internal/composio/server.go`, `demo.go` |
| Binary | `backend/cmd/composio-broker/main.go` |
| Migration | `backend/migrations/154_composio_integrations.sql` |
| Tests | `backend/internal/composio/proxy_test.go` |

Composio user id: `dada:<project-uuid>:<channel>:<external-id>` -- derived from
primary keys, never an email, because Composio keys every connected account on
that string.

## Agent-side claim

```yaml
tools:
  - name: composio-<project-slug>
    url: http://composio-broker.dada-cloud.svc.cluster.local:8085/mcp/<project-uuid>
    protocol: STREAMABLE_HTTP
    allowedHeaders:
      - x-dada-end-user
      - x-dada-agent
```

Without those two `allowedHeaders` entries the runtime drops the headers and the
broker refuses every call with "no end user on this call" -- by design, since the
alternative is serving one user's tools to another.

## Env of the broker

| Var | Meaning |
|---|---|
| `COMPOSIO_API_KEY` | platform key, k8s Secret, never in values or git |
| `COMPOSIO_BROKER_DB_URL` / `DB_URL` | console DB |
| `COMPOSIO_BROKER_PORT` | default 8085 |
| `COMPOSIO_TOOLKITS` | curated app list |
| `COMPOSIO_BROKER_PUBLIC_URL` | where the OAuth callback returns |
| `COMPOSIO_BROKER_BASE_PATH` | prefix under a reverse proxy |
| `COMPOSIO_BROKER_TOKEN` | guards the browser-facing half |

## Not done yet

- Helm chart + prod values + Secret (the deploy path is
  `references/agent-harness-deploy-path.md`).
- Console Next.js Integrations page (the demo page proves the three endpoints).
- Console REST proxy so the browser talks to the console's authenticated API
  instead of the broker's token.
- `composio.connected_account.expired` webhook -> notify the user to reconnect.
- Cost: tool calls are `$0.0003` each plus `$0.0002` on Composio-managed OAuth
  apps `[live composio.dev/pricing 2026-09-12]`. `composio_tool_calls` is the
  counter; nothing bills off it yet.
- Own OAuth apps so the consent screen says DADA Cloud rather than Composio.
- Data residency: with managed auth a Russian client's Gmail token lives in a US
  SaaS and payloads are retained 7d/30d unless ZDR (a Pro add-on) is bought.
