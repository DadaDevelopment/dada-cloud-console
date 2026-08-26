# Dada Cloud — Claude Code plugin

Connect Claude Code to [Dada Cloud](https://console.dada-tuda.ru) with one command.
Installs an MCP server exposing 47 curated platform tools — apps, databases,
domains, builds, logs, and Dada Box (an ephemeral root sandbox you get in one
call) — plus guided prompts: `deploy-app`, `configure-env`, `diagnose-app`.

The backend reflects its whole REST surface into tools and then curates it down
with an allowlist (`backend/internal/mcp/default_overrides.yaml`), so the tool
list stays the loop an agent actually needs instead of growing every time the API
does. The number above is that allowlist's length, and a test keeps the two in
step.

## Install

```
/plugin marketplace add DadaDevelopment/dada-cloud-console
/plugin install dada-cloud@dada-cloud
```

On first use Claude Code opens your browser to log in at
`id.dada-tuda.ru` (Dada ID). Nothing to type — no URL, no client id, no token.

## How auth works

The plugin runs [`mcp-remote`](https://github.com/geelen/mcp-remote) as a local
stdio bridge to the remote server `https://console.dada-tuda.ru/mcp`. It performs
OAuth 2.1 Authorization Code + PKCE against Dada ID using the pre-registered
public client `dada-mcp` (no secret). This pins the `client_id` and the scope
set, so it works without Dynamic Client Registration — sidestepping the Claude
Code native-OAuth limitation. The token is stored locally by `mcp-remote` and
refreshed automatically.

The pin works from any machine because `mcp-remote` takes its callback on
loopback (`http://localhost:<port>`), and `dada-mcp` whitelists loopback on any
port. Nobody has to register anything per user.

## Do not copy this config into a hosted agent

The `client_id: dada-mcp` line above is safe here and wrong anywhere the agent
runs on a server. A hosted agent — a self-hosted gateway, a remote harness — has
to take its OAuth callback on a public `https` URL of its own, and that URL is
in nobody's whitelist, so Dada ID answers `invalid_redirect_uri` before the
sign-in page renders. Registering one callback per installation does not scale,
and Dynamic Client Registration is still closed on Dada ID.

Until it opens, a server-side agent talks to the same endpoint with a bearer
token (`Authorization: Bearer <dada-id-token>`), which the MCP server accepts.
See [Control DADA Cloud from an AI agent](https://console.dada-tuda.ru/docs/mcp-ai-agents).

You only ever see resources your Dada ID account has a role on; all tool calls
carry your bearer and are authorized by the backend per project.

## Requirements

- Node.js (for `npx mcp-remote`)
- A Dada Cloud account at https://console.dada-tuda.ru
