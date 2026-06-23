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

## Connect-repo HTTP endpoints — partial

`build-agent/internal/server/server.go` now serves the two server-to-server
endpoints the console backend calls during the wizard:
- `GET /github/installations/{id}/repos` → `github.App.ListRepos` (real listing).
- `GET /github/installations/{id}/detect` → best-effort, returns an all-null
  `FrameworkDetection` (real Nixpacks detection needs a clone and belongs in the
  build Job; the wizard falls back to manual framework selection).

So once a `git_app_installations` row exists, the wizard's pick-repo step works.

### STILL MISSING — installing an account (populating git_app_installations)
- `GetGitInstallURL` (backend) returns `BUILD_AGENT_URL + /github/install?state=`,
  but `BUILD_AGENT_URL` is the **internal** cluster address — a browser cannot reach
  it. The install URL must be browser-reachable: either return the GitHub public
  `https://github.com/apps/<slug>/installations/new?state=<projectId:nonce>` directly
  (needs the app slug in config), or expose an agent `/github/install` redirect via
  public ingress.
- No **install callback** exists to write `git_app_installations` (project_id,
  provider, installation_id, account_login, account_type) after the user installs the
  App. GitHub redirects the browser to the App's configured Setup URL with
  `installation_id` + `state`; that endpoint (public) must persist the row. Deciding
  where it lives (backend has the DB + is public; build-agent has the App private key
  to resolve account login via `GET /app/installations/{id}`) is the open design call.
- Public ingress: GitHub → `/webhook/github` and the install callback both need a
  public path to the agent (or backend). Add to `ingress.yaml` when wiring go-live.

Until the install/callback path lands, accounts can't be connected from the UI, so
the repo list has nothing to list. The webhook → build → CreateApp/DeployImageVersion
path is complete and works once a `git_repos` row exists.

## Verify after enabling
- `kubectl get deploy <release>-build-agent` Ready; `/healthz` 200.
- Backend git endpoints stop returning 503 (e.g. `GET …/git/install-url?provider=github`).
- Push to a connected repo → a `builds` row goes `queued→…→success` → a `CreateApp`
  (first time) or `DeployImageVersion` operation appears and the app goes Ready.
