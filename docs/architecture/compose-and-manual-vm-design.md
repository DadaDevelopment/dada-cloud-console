# Compose Applications, Manual VM Connect & Live State — Design

Status: Approved (design); implementation phased
Date: 2026-06-02
Scope: `backend` (Go/Gin), `portainer-agent` (Go + Terraform + SSH), `gitops-agent` (Go), `frontend` (Next.js)

## Context

Production today: `cloud-console → Portainer (CE) → Edge Agent → VM`.
- VMs are provisioned via Terraform (Beget/OpenStack) + cloud-init that installs Docker + Portainer Edge Agent (`portainer-agent/internal/worker/create_appserver.go`, ADR-007).
- Helm apps are GitOps: backend signs a 90s `wstoken` → frontend WS to `gitops-agent /ws/values` → edits `values.yaml` in git → ArgoCD reconciles to the K8s cluster.
- `portainer-agent/internal/portainer/client.go` already has `CreateStackFromGit`, `RedeployStack`, `ListStacks`, `ListContainers`, `GetEndpoint`, `IsAgentConnected`, `StreamLogs`.

We add three capabilities:
1. **Manual VM connect** — register an existing (non-Terraform) VM by SSH-pushing the existing edge-agent bootstrap.
2. **Compose application** — a Docker-Compose workload as an `App` runtime variant, GitOps-style (`compose.yaml` + `.env` in git), deployed to a Portainer endpoint via `CreateStackFromGit`, with a two-pane editor.
3. **Live state** — console shows VM + stack/container state by proxying the Portainer API live (read-only).

## Understanding Summary

- **What:** three additions to the existing control plane (above).
- **Why:** let customers run Compose stacks on their own VMs (Terraform or hand-connected) and see runtime state, in-console.
- **Who:** project members with write role (`canWrite`) deploy/edit; all members view.
- **Build order:** ① Manual VM → ② Compose deploy+editor → ③ Live state.

## Assumptions (confirmed)

- **A1:** Manual-VM SSH key is supplied in the create request, used once, never persisted long-term. Transits via `operations.payload`; scrubbed on terminal state.
- **A2:** Compose is **another `App` runtime variant** (not a separate kind).
- **A3:** Compose App is bound to a specific AppServer (VM/endpoint), chosen at create. Two-pane editor (`compose.yaml` + `.env`) ships in v1.
- **A4:** Live state proxy is read-only in this scope (no start/stop/redeploy).
- **A5:** `bootstrap.sh.tmpl` installs Docker; Ubuntu/root assumption unchanged.

## Design

### 1. Data model & git layout

`App` gains a `runtime` discriminator (`helm` | `compose`) and `app_server_id` (nullable; required when `runtime=compose`).

- **helm App** (today): `application.yaml` + `chart/` → ArgoCD → K8s. Unchanged.
- **compose App** (new): `compose.yaml` + `.env` in the same app tree; targets a Portainer endpoint; deployed by `portainer-agent`, not ArgoCD.

Git layout (reuses `gitops-agent/internal/renderer` path helpers; adds two):
```
clusters/beget-prod/projects/{project}/environments/{env}/apps/{app}/
  helm:    application.yaml, chart/{Chart.yaml,values.yaml,templates/}
  compose: compose.yaml          ← new AppComposeGitPath()
           .env                   ← new AppEnvGitPath()
```
No ArgoCD `Application` CRD for compose apps — nothing reconciles them into the cluster. Git stays source of truth; `portainer-agent` is the reconciler.

`AppServer` (`models/appserver.go`) gains a `Source` field (`terraform` | `manual`). Manual VMs are rows with `vm_provider_id=NULL`, `terraform_workspace=NULL`.

### 2. Deploy trigger model (operation-queue driven)

Consistent with the rest of the system:
- `CreateApp(compose)` → `gitops-agent` renders+commits `compose.yaml`/`.env`, then enqueues a `DeployStack` op → `portainer-agent` runs `CreateStackFromGit` on the bound endpoint.
- Editor save (commit) → `gitops-agent` enqueues `DeployStack(redeploy)` → `portainer-agent` `RedeployStack` (re-pulls from git).

Explicit deploy events + audit, mirroring how ArgoCD reconciles helm. `portainer-agent` (VMWatcher) handles `DeployStack` ops; `gitops-agent` (DBWatcher) handles render/commit ops.

### 3. Feature flows

**① Manual VM** — `portainer-agent` new `doCreateManualAppServer` = `doCreateAppServer` minus Terraform steps 3–4:
1. `CreateEdgeEndpoint` (unchanged)
2. `CreateAppServer` row (ip set, no workspace, `source=manual`)
3. `SetAppServerProvisioned(ip, provider_id=nil)`
4. `ssh.RunBootstrap(ip, ssh_user, payload.ssh_private_key, params)` — reused as-is
5. `waitForAgent(ep.ID)` → `SetAppServerReady`

Backend `CreateAppServer` payload gains `mode` (`terraform` | `manual`) + manual fields `{vm_ip, ssh_user, ssh_port?, ssh_private_key}`.
**Secret handling:** key transits `operations.payload` (DB-persisted); the `ssh_private_key` field is scrubbed (overwritten) on terminal state (Ready/Failed).

**② Compose deploy + editor** — deploy per §2. Editor: generalize `gitops-agent` `handleValuesWS` → `handleFileWS` keyed by a `file` claim (`values.yaml` | `compose.yaml` | `.env`); YAML syntax check only for `*.yaml`, skipped for `.env`. Backend `GetValuesToken` accepts a `file` param, signs it into `wstoken.Claims.File`. Frontend two-pane = two WS connections (one per file), reusing the existing editor component.

**③ Live state** (read-only) — add a lean read-only Portainer client to `backend/internal/portainer` (services are separate Go modules → duplicate ~4 methods rather than couple). New endpoints:
- `GET .../app-servers/:name/state` — endpoint heartbeat + containers
- `GET .../apps/:name/state` — stack + container status
- `GET .../apps/:name/logs` — proxy `StreamLogs`

### 4. Editor token generalization

`wstoken.Claims` gains `File string`. `gitops-agent` verify must match `claims.File == query.file` (prevents a values-token editing compose). Path dispatch:
```
values.yaml  → AppHelmValuesGitPath
compose.yaml → AppComposeGitPath   (new)
.env         → AppEnvGitPath       (new)
```

## Decision Log

| Decision | Alternatives | Why |
|---|---|---|
| Compose = `App` runtime variant | Separate `ComposeApp` kind | A2; reuses App CRUD/list/editor wiring |
| Deploy via operation queue (commit→`DeployStack`) | Portainer git auto-poll | Status visibility + audit, matches existing op model |
| Git→Portainer reconciled by `portainer-agent` | ArgoCD plugin | ArgoCD targets K8s, not Docker endpoints |
| Backend gets own read-only Portainer client | Call `portainer-agent` HTTP; shared module | Services are independent modules today; least coupling |
| SSH key scrubbed from payload on terminal state | Encrypt+store; never persist (needs side channel) | Honors one-shot with minimal complexity |
| Two WS connections for two-pane | One multiplexed WS | Reuses single-file handler untouched |

## Risks & Non-goals

- **R1:** `.env` secrets land in git plaintext — same trust model as helm `values.yaml` today. Accepted for v1.
- **R2:** Compose app on a non-`Ready` AppServer — guard at create (require `Ready`).
- **R3:** No start/stop/redeploy buttons in v1 (state read-only, A4).
- **R4:** `CreateStackFromGit` needs the git repo reachable by Portainer with creds — verify Portainer has the same repo access the agent does.

**Non-goals:** rollback UI, compose→helm migration, multi-VM stack spread, persisting SSH creds.
