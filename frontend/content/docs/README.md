# dada-cloud console — user guides

Task-oriented how-tos for the console, written directly against the current UI (not the
product roadmap). Where a guide found the console behaving differently than
`docs/product/feature-inventory.md` describes, that's called out in a note at the top of the
guide — treat the guide as the source of truth.

## Core

Deploy and run an application.

- [Deploy an app from GitHub](applications-deploy-from-github.md)
- [Deploy from an image or Compose](applications-deploy-image-and-compose.md)
- [Add a "Deploy on Dada" button to your repository](deploy-button-badge.md)
- [Managed Postgres databases](databases-postgres.md)
- [Custom domains and HTTPS](domains-and-https.md)
- [Monitoring: metrics, logs, alerts](monitoring-metrics-logs-alerts.md)

## AI agents (MCP)

Drive the platform from Claude or any other MCP client.

- [Control DADA Cloud from an AI agent (MCP)](mcp-ai-agents.md) — endpoint, auth,
  and how to connect each client.
- [MCP tool reference](mcp-tool-reference.md) — all 56 tools, their arguments, and
  what is deliberately not exposed.
- [MCP recipes: worked flows](mcp-recipes.md) — the exact tool sequences behind
  deploy, database, sandbox and diagnose.

## Servers (App Servers / VMs)

Bring or order a VM and run workloads on it directly.

- [Bring your own VM](app-servers-bring-your-own-vm.md)
- [Order a managed VM](app-servers-order-a-vm.md)
- [Adopt existing workloads on a VM](app-servers-adopt-existing-workloads.md)
- [Running a fleet of client VMs (agency playbook)](app-servers-agency-fleet.md)

## Advanced

Team, billing, and platform extras.

- [Object storage (S3-compatible)](object-storage.md)
- [Members and roles](members-and-roles.md)
- [Billing, plans, and limits](billing-plans-and-limits.md)
- [Builds and deployments](builds.md)
- [AI Models (model serving)](ai-models.md)
- [AI model approvals (GPU approval queue)](ai-model-approvals.md)

## How these were written

Each guide covers, in plain language:

- **What it's for** — the job you're trying to get done.
- **How to** — imperative, step-by-step instructions matching what's actually on screen today.
- **Gotchas** — non-obvious behavior that will trip you up.
- **Not yet supported** — an honest list of what the UI can't do yet, so you stop looking for
  it.
