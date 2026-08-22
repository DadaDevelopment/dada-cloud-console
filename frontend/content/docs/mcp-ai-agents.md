# Control DADA Cloud from an AI agent (MCP)

## What it's for

Run the platform by talking to an AI. DADA Cloud ships a Model Context Protocol
(MCP) server, so an assistant like Claude can create projects, deploy apps, set
environment variables, spin up sandboxes, manage databases and domains, and read
your logs — on your behalf, using your own login. You ask in plain language
("deploy this image and give it a Postgres"); the agent calls the right platform
tools and reports back.

The server exposes **42 curated tools** drawn from the same API the web console
uses. It is not the whole API: the tool list is an allowlist, chosen so an agent
carries a surface it can actually reason about. The agent only ever sees projects
your account has a role on, and every action is authorized exactly as if you had
clicked it yourself.

- Every tool and argument: [MCP tool reference](mcp-tool-reference.md)
- Worked end-to-end flows: [MCP recipes](mcp-recipes.md)

## Endpoint and identity

| | |
| --- | --- |
| MCP endpoint | `https://console.dada-tuda.ru/mcp` |
| Transport | Streamable HTTP |
| Auth | OAuth 2.0 (browser sign-in with DADA ID) or a bearer token |
| OAuth client id | `dada-mcp` — public, **no client secret** |
| Authorization server | Discovered automatically (RFC 9728 metadata) |

Hitting the endpoint without a token returns `401` with a `WWW-Authenticate`
header pointing at the metadata document. That is the handshake working, not an
outage: a spec-compliant client reads it and starts the browser login by itself.

## Connect from Claude Code

The fastest path — nothing to paste, no keys.

1. Add the marketplace:
   ```
   /plugin marketplace add DadaDevelopment/dada-cloud-console
   ```
2. Install the plugin:
   ```
   /plugin install dada-cloud@dada-cloud
   ```
3. On first use your browser opens to sign in with your DADA ID account. Approve,
   and the tools are ready.

The plugin runs a small local bridge that performs the standard browser login for
you, so you never handle a URL, a client id or a token.

## Connect from Claude Desktop

1. **Settings → Connectors → Add custom connector**.
2. URL: `https://console.dada-tuda.ru/mcp`
3. Open **Advanced settings** and set **OAuth Client ID** to `dada-mcp`. Leave
   **Client Secret** empty.
4. Save. On first use your browser opens to sign in with DADA ID.

## Connect from Cursor, Windsurf, or any other MCP client

Point the client at `https://console.dada-tuda.ru/mcp` over Streamable HTTP.

Clients that let you pin a static OAuth client id should use `dada-mcp` (public,
no secret). Clients that discover it dynamically need nothing from you.

If your client cannot do a browser login at all, send a DADA ID access token as a
header instead:

```
Authorization: Bearer <your-token>
```

To check the server is reachable before you blame your client, ask it for its
discovery document. This needs no credentials:

```
curl -sS https://console.dada-tuda.ru/.well-known/oauth-protected-resource
```

```
{"resource":"https://console.dada-tuda.ru/mcp",
 "authorization_servers":["https://id.dada-tuda.ru/realms/master"],
 "scopes_supported":["openid","profile","email","offline_access",
                     "read","builds:read","builds:write","deploy:write"],
 "bearer_methods_supported":["header"]}
```

A `401` from the endpoint itself, carrying
`WWW-Authenticate: Bearer resource_metadata="…"`, is the same handshake seen from
the other side. Both mean the server is up.

## What the agent gets besides tools

The server also serves the two other halves of MCP, which good clients surface in
their UI.

**Prompts** — guided runbooks with arguments, which walk an agent through a
multi-step flow instead of leaving it to improvise:

| Prompt | Arguments | What it does |
| --- | --- | --- |
| `deploy-app` | `project`, `name`, `image`, `port` | Confirm the project, create the app, set its variables, then poll until the app is really Healthy. |
| `configure-env` | `project`, `app`, `vars` | Inspect existing keys, apply changes one key at a time, confirm the rollout. |
| `diagnose-app` | `project`, `app` | Read the phase, pull logs, check the latest build, and explain the root cause without mutating anything. |

**Resources** — read-only reference material an agent can pull for grounding:

| Resource | What it is |
| --- | --- |
| `dada://guide/getting-started` | How the platform models apps, variables, domains and async operations. |
| `dada://reference/tools` | The live tool index with one-line summaries. |
| `dada://reference/openapi.json` | The full OpenAPI document the tools are generated from. |

## Permissions and safety

- **The agent acts as you.** It can only touch projects your account has a role
  on, and write actions still require the matching role — exactly like the
  console. There is no service identity and no elevation.
- **Two tools are marked destructive** (`deleteBox`, `deleteEnvVar`) and 20 are
  marked read-only. Clients that honour those hints will ask before running a
  destructive one.
- **There is no way to delete an app, a database or a project** from the agent
  surface, on purpose.
- **Secret env values are never readable back.** `listEnvVars` masks them.
- **The agent cannot run commands inside your sandbox.** A box hands out its own
  local MCP endpoint that you add to your client as a second server, so your code
  and your model credentials never pass through our API.
- **Credential reveals are audited.** `getDatabaseCredentials` requires an
  explicit `reveal=true` and records every call.

## Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| `401` from the endpoint, no login prompt | The client is not doing OAuth discovery | Set the OAuth client id to `dada-mcp` manually, or send a bearer token |
| `invalid_scope` before the login page renders | The client asked for scopes the public client cannot grant | Make sure the client reads the protected-resource metadata rather than the authorization server's full scope list |
| Tools appear but every call 404s | `projectId` was given as a slug | `projectId` and `envId` are UUIDs — call `listProjects`, then `getProject` |
| `missing required path parameter "envId"` | Nothing supplied the environment id | `getProject` returns the project's environments with their ids |
| Operation says Committed but nothing is running | Committed means written, not up | Poll `listApps` for the app phase; if it never reaches Healthy, call `searchLogs` |
| `getDatabaseCredentials` returns 404 | The database is still provisioning, so no secret exists yet | Poll `listDatabases` until the phase is ready, then retry |
| Session dies after a while | Access tokens are short-lived | The browser login refreshes automatically; re-run the login if the client dropped the refresh token |
| An action you expected is missing | It is not on the allowlist | Check the [tool reference](mcp-tool-reference.md); the REST API still has it |

## Gotchas

- Sign in with the same DADA ID account you use for the console at
  `console.dada-tuda.ru`.
- Deploys and other changes are asynchronous. The agent gets an operation to
  watch, and should confirm the app is Healthy before declaring success.
- The one-time `dadabox_` session token a box returns is shown exactly once and
  is never retrievable — only its hash is stored. Mint a new one with
  `getBoxConnection` if you lose it; minting does not revoke the old one.
