# Dada Cloud — Claude Code plugin

Connect Claude Code to [Dada Cloud](https://console.dada-tuda.ru) with one command.
Installs an MCP server exposing 41 curated platform tools — apps, databases,
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

You only ever see resources your Dada ID account has a role on; all tool calls
carry your bearer and are authorized by the backend per project.

## Requirements

- Node.js (for `npx mcp-remote`)
- A Dada Cloud account at https://console.dada-tuda.ru
