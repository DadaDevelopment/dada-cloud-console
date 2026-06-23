# Enabling git → build → deploy (build-agent)

Status: enablement runbook
Companion design: `tasks/vercel-flow-design.md`, `tasks/vercel-flow-impl-plan.md`

This is the "Deploy from Git" engine. When it is off, every git endpoint on the
console backend returns `503 git integration not configured` and the wizard shows
"git integration not configured". That 503 is purely because `BUILD_AGENT_URL` is
empty (`backend/internal/api/handler.go` → `h.buildagent == nil`).

## What the product flow expects

1. User picks **Applications → Deploy from Git** (or **Builds → Connect repository**).
2. Connect a GitHub account → pick a repo → name the app + port/profile.
   **No app is created yet, and no placeholder image is deployed.**
3. On the first push to the production branch, build-agent builds the repo, pushes
   an image, and the deploy handoff (`build-agent/internal/db/deploy.go:HandoffDeploy`)
   enqueues:
   - **`CreateApp`** (with the real image + the stored port/replicas/profile) when the
     app does not exist yet — this is what *materializes* the app, and
   - **`DeployImageVersion`** on every subsequent build.
4. The existing gitops rails (gitops-agent → Argo → pod) take it from there.

## Required to go live (all of these — the chart alone is not enough)

### 1. Deploy the build-agent (chart, done)
```yaml
# values.yaml
buildAgent:
  enabled: true
global:
  shared:
    buildAgentTokenSecret: "<openssl rand -hex 32>"   # backend mints, agent verifies
```
Enabling it auto-wires the backend: `configmap.yaml` sets
`BUILD_AGENT_URL=http://<release>-build-agent:8091` and `BUILD_AGENT_WS_URL=wss://<ingress.host>`,
and both the backend Secret and the build-agent Secret get the same
`BUILD_AGENT_TOKEN_SECRET`. The 503 disappears once `BUILD_AGENT_URL` is non-empty.

`GITOPS_ENCRYPTION_KEY` in `buildAgent.secret` **must equal**
`gitopsAgent.secret.GITOPS_ENCRYPTION_KEY` (shared AES-GCM key).

### 2. Register a GitHub App (external, one-time)
Least-privilege permissions: Contents R, Metadata R, Commit statuses R/W, Checks R/W,
Pull requests R, Webhooks. Events: `push`, `pull_request`. Webhook URL →
`https://<ingress.host>/api/v1/webhooks/github` (proxied to the agent's
`/webhook/github`). Fill `BUILD_GITHUB_APP_ID`, `BUILD_GITHUB_APP_KEY` (PEM),
`BUILD_GITHUB_WEBHOOK_SECRET`.

### 3. Build backend (Jenkins) + registry (Nexus)
The runner (`build-agent/internal/worker/runner.go`) drives a Jenkins job
(`JENKINS_*`) that builds and pushes to Nexus (`NEXUS_*`); the agent confirms the
image by digest. The Jenkins pipeline must emit the contract marker
`==> image: <host>/<proj>/<app>@sha256:<digest>`.

## KNOWN GAP — connect-repo HTTP endpoints are not implemented yet

The build-agent HTTP server (`build-agent/internal/server/server.go`) currently
serves only `/healthz`, `/metrics`, `/webhook/github`, `/ws/build`.

The backend's connect-repo flow calls endpoints the agent does **not** yet serve:
- `GET /github/install` (install landing/redirect) — backend `gitrepos.go:GetGitInstallURL`
- `GET /github/installations/:id/repos` — `buildagent.ListInstallationRepos`
- `GET /github/installations/:id/detect` — `buildagent.DetectFramework`

So with the agent deployed, the **webhook → build → deploy** path works, but the
**connect a repo in the UI** path (list accounts/repos, framework detect) will fail
until those handlers are added (the building blocks exist: `github/app.go` has
`ListRepos`/`InstallToken`, and the `detect` package exists — they just aren't wired
to HTTP routes, and the OAuth install callback that populates
`git_app_installations` is not implemented). Track as the next build-agent task.

## Verify after enabling
- `kubectl get deploy <release>-build-agent` Ready; `/healthz` 200.
- Backend git endpoints stop returning 503 (e.g. `GET …/git/install-url?provider=github`).
- Push to a connected repo → a `builds` row goes `queued→…→success` → a `CreateApp`
  (first time) or `DeployImageVersion` operation appears and the app goes Ready.
