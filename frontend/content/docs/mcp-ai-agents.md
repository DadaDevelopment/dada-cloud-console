# Control DADA Cloud from an AI agent (MCP)

## What it's for

Run the platform by talking to an AI. DADA Cloud exposes a Model Context Protocol
(MCP) server, so an assistant like Claude can create servers, deploy apps, set
environment variables, manage databases and domains, and read your logs and
metrics — on your behalf, using your own login. You ask in plain language
("spin up a server and deploy my repo"); the agent calls the right platform
actions and reports back.

It is the same API the web console uses (132 actions), wrapped as agent tools.
The agent only ever sees the projects your account has a role on, and every
action is authorized exactly as if you clicked it yourself.

## What you can ask it to do

- Create and manage apps (deploy from an image, restart, roll back).
- Read and set environment variables (secrets are stored encrypted).
- Create databases and manage domains / HTTPS.
- Trigger builds and stream build and runtime logs.
- Read metrics and recent operations to diagnose a failing app.

The server also ships three guided prompts your agent can use directly:
`deploy-app`, `configure-env`, and `diagnose-app`.

## Connect from Claude Code (one command)

This is the fastest path — nothing to type, no keys.

1. Add the marketplace:
   ```
   /plugin marketplace add DadaDevelopment/dada-cloud-console
   ```
2. Install the plugin:
   ```
   /plugin install dada-cloud@dada-cloud
   ```
3. On first use your browser opens to log in with your DADA ID account. Approve,
   and the tools are ready.

Under the hood the plugin runs a small local bridge that performs the standard
browser login for you, so you never paste a URL, client id, or token.

## Connect from Claude Desktop

1. Settings → **Connectors** → **Add custom connector**.
2. URL: `https://console.dada-tuda.ru/mcp`
3. Open **Advanced settings** and set **OAuth Client ID** to `dada-mcp`. Leave
   **Client Secret** empty.
4. Save. On first use your browser opens to log in with DADA ID.

## Connect any other MCP client

Point your client at `https://console.dada-tuda.ru/mcp`. Clients that let you set
a static OAuth client id should use `dada-mcp` (public, no secret). If your client
can't do the browser login, send a DADA ID access token as a header instead:

```
Authorization: Bearer <your-token>
```

## Gotchas

- The agent acts as you. It can only touch projects your account has a role on,
  and write actions still require the matching permission — exactly like the
  console.
- Log in with the same DADA ID account you use for the console at
  `console.dada-tuda.ru`.
- Access tokens are short-lived; the browser login refreshes them automatically.
  If a session expires, the client re-runs the login.
- Deploys and other changes are applied asynchronously — the agent gets an
  operation to watch, and confirms once the app reports healthy.
