# Discovery: VM Import → Docker Compose Management → UI GitOps / Dada Stack

> Read-only discovery. Nothing implemented, nothing changed. All claims tied to file paths.
> Author: discovery pass, 2026-07-03. Method: direct code read + 4 parallel explorer agents
> (backend, frontend, agents, docs).

---

## 1. Executive summary

**Bottom line: ~60–70% of this feature already exists in the tree.** Dada Cloud already
ships a "VM track": manual-connect an existing VM over SSH, drop a Portainer Edge Agent on
it, deploy a Docker **Compose** app from git to that VM, edit `compose.yaml` + `.env` in a
two-pane live editor, and read back live container state + logs. That is exactly the spine
of the requested product.

What is **missing** is the "import existing workload" and "true GitOps loop" half:
- **Reverse import** (`docker ps` → generated `compose.yaml` → git) exists only as a
  read-only **bash SSH prototype** (`scripts/vm-discover.sh`), not as an agent capability or
  a UI flow.
- **No Docker/compose discovery** as an agent verb (no container/image/volume/network
  inventory through the cloud path).
- **No container lifecycle** through the cloud (no stop/remove/exec) — the agent is
  create/delete-only. This is the explicit open blocker in `tasks/vm-gitops-migration-plan.md`
  (Phase 4 "decision A vs B").
- **No drift detection**, no desired-vs-actual reconcile for compose, no compose rollback UI.
- **No first-class entities** for imported stacks / services / volumes / networks / runtime
  containers — a VM "app" is one Portainer stack, opaque below that.

**Readiness by layer:**

| Layer | Readiness | Note |
|---|---|---|
| VM connect (manual + terraform + adopt) | **Ready** | shipped, migrations 004/009/012 |
| Compose deploy from git → Portainer | **Ready** | `doDeployStack` full |
| compose.yaml + .env editor (WS, two-pane) | **Ready** | `handleFileWS`, `/compose` page |
| Live container state + logs (read-only) | **Ready** | `GetAppState`, `ListContainers` |
| Env/secret management | **Partial** | `.env` in git plaintext; env vars UI exists |
| Docker discovery / import existing compose | **Missing** (bash prototype only) | `scripts/vm-discover.sh` |
| Container lifecycle (stop/remove/exec) | **Missing** | agent is declarative-only |
| GitOps loop for compose (diff/drift/rollback) | **Missing** | git is source of truth but no reconcile/drift |
| Multi-server single stack | **Missing** | one app = one endpoint |

**Strategic tension to resolve first (not a code gap):** the canonical product vision
(`docs/product/product-gtm-vision.md`) explicitly says *do not* sell "another Coolify /
infra / GitOps / Kubernetes." It says sell **outcomes** ("GitHub → backend → Postgres →
domain → HTTPS → rollback"). The requested feature is literally "grab the Coolify /
Portainer / Komodo audience." Those two framings can both win, but the messaging bet must be
decided before building UI copy. See §14.

---

## 2. Existing product vision / docs

### `docs/product/product-gtm-vision.md` — CANONICAL vision (north star)
- Product bet: **"Backend Cloud for startups and small teams without DevOps"**, NOT
  "simplified Kubernetes." One path: `GitHub → backend → Postgres → домен → HTTPS → логи →
  rollback`.
- ICP: solo-founder / vibe coder, small startup (2–10), small agency, backend-dev/tech-lead.
- Year-1 KPI = **activation**: median time-to-first-deploy ≤ 10 min, first-deploy success
  ≥ 60%, onboarding completion ≥ 40%.
- Competitor benchmark: Vercel / Railway / Render (import-repo autodeploy, previews, custom
  domains + TLS, zero-config Postgres, logs, fast rollback, templates).
- **Explicit warning**: no "GitOps / Crossplane / ArgoCD / Kubernetes" language in hero;
  sell outcomes. Telemetry/IoT/VM lines are "adjacent bets", must not dominate messaging.
- Directly relevant customer sentence: current customers run *"VPS + Docker Compose + Claude"*
  and *"GitHub Actions + Compose/k8s/Render"* — this VM/Compose feature targets exactly that
  migration pain.

### `docs/architecture/compose-and-manual-vm-design.md` — THE design doc for this feature
- Status: **Approved (design); implementation phased**, 2026-06-02.
- Adds three capabilities, ALL now at least partially built:
  1. **Manual VM connect** (SSH-push edge agent to an existing VM).
  2. **Compose application** = App runtime variant, git `compose.yaml` + `.env`, deployed to
     a Portainer endpoint, two-pane editor.
  3. **Live state** = read-only Portainer proxy (endpoint heartbeat + containers + logs).
- Key decisions: compose = App runtime variant (not new kind); deploy via operation queue
  (commit → `DeployStack`); `portainer-agent` reconciles git→Portainer (ArgoCD is k8s-only);
  SSH key one-shot scrubbed from `operations.payload`; two WS connections for two-pane.
- **Non-goals (v1)** stated in doc: rollback UI, compose→helm migration, multi-VM stack
  spread, persisting SSH creds. → These are the exact gaps §4–6 confirm.

### `tasks/vm-gitops-migration-plan.md` — reproducible VM→GitOps cutover plan (fin-data first)
- 6-phase flow: DISCOVER (read-only SSH) → AUTHOR gitops compose → ENROLL edge agent →
  REHEARSE → CUTOVER → LOCK-IN.
- Core risk documented in detail: PG named-volume data loss on first `compose up`; mitigated
  by `external: true` + literal `name:` auto-emitted by `scripts/vm-discover.sh`.
- **Phase 4 open decision (the key missing verb):** cloud needs stop/remove of hand-run
  containers before stack deploy. Options A (add `StopContainer`/`RemoveContainer` +
  "adopt VM" worker op — recommended), B (operator does it in Portainer UI — interim),
  C (rejected). **This is the single most important backend gap for the import story.**

### `docs/adr/ADR-007-portainer-edge-agent-runtime.md` — runtime tech choice
- Decision: **Portainer CE + Edge Agent** as the remote Docker runtime layer. Explicitly
  **rejected Coolify** ("UI-first, hard to embed, opinionated"), Dokku, Nomad, custom SSH.
- "What DADA does NOT build": Docker orchestration, compose execution, log streaming, tunnel,
  agent reconnect, state sync. Portainer owns all of that. → Reuse posture is deliberate.

### `README.md` + `dada_cloud_console_adr_prd_v_1_roadmap.md` — platform spine
- Core loop: `UI → Backend → DB → Operation → gitops-agent (render+commit) → Argo/Portainer →
  status back to UI`. Typed actions only, no raw YAML for clients (ADR-004).
- Roadmap: v1 ServiceDatabase; v2 App lifecycle + gateway + **Portainer Compose on VMs**;
  v3 multi-tenant/quotas/plans; v4 marketplace/storage/AI.
- Product principle (quote): *"The platform owns the complexity. The console sells
  simplicity."*

### Other relevant docs
- `docs/plans/2026-05-13-gitops-agent.md` — the git render/commit/watch service (how
  compose.yaml/.env get committed and how manual git commits sync back to `resource_snapshots`).
- `docs/plans/2026-05-08-v2-app-lifecycle.md` — `CreateApp` / `DeployImageVersion` typed-action
  pattern (the template any new compose action follows).
- `docs/adr/ADR-010-jenkins-as-a-service.md`, `ADR-011-monitoring...`, `ADR-012-telemetry-gateway.md`
  — build + observability substrate (reusable for VM apps).
- `docs/prd/PRD-monitoring.md`, `PRD-IAM.md` — monitoring & roles baseline.

---

## 3. Existing architecture

**Control plane (Go):**
- `backend` (Go/Gin) — REST API `/api/v1`, RBAC, operation queue writer, read-only Portainer
  client for live state, monitoring read layer. Entry `backend/cmd/server`.
- `gitops-agent` — owns git: DB-watcher renders manifests + commits (`internal/worker/dbwatcher.go`),
  git-watcher syncs manual commits back to `resource_snapshots` (`internal/worker/gitwatcher.go`),
  WS file editor (`internal/server/ws_handler.go`), webhook server.
- `portainer-agent` — talks to Portainer CE + Terraform + SSH. VM lifecycle + stack deploy
  (`internal/worker/vm_watcher.go`, `create_appserver.go`, `deploy_stack.go`).
- `build-agent` — GitHub App, webhooks, framework detect, Jenkins build orchestration,
  handoff-deploy (`internal/worker/runner.go`, `internal/db/deploy.go`).
- `mcp-server` — reflective MCP over `/api/v1` (out of scope here).
- Two more gateways: `backend/cmd/gateway` (OTLP telemetry), `backend/cmd/grafana-embed-gateway`.

**Data / substrate:**
- Postgres (single DB, migrations `backend/migrations/001..028`).
- Git state repo `clusters/beget-prod/projects/{project}/environments/{env}/apps/{app}/...`
  is the desired state.
- Runtime: k8s (via ArgoCD) **or** VM/Docker (via Portainer Edge Agent) — chosen per
  **Environment** (`runtime = k8s | vm`).

**Communication model = operation queue.** Every mutation is an `Operation` row
(`backend/internal/models/operation.go:190`) with a state machine
(`Created → Queued → Rendering → CommittingToGit → Committed → WaitingForArgoSync → Syncing →
Reconciling → Ready`). Agents poll `operations` with `FOR UPDATE SKIP LOCKED`. Typed payloads
per action (see §8).

**Auth / tenancy:** OIDC (Keycloak), org "dada" single-org collapse, project-scoped RBAC
(`canWrite`, scopes `builds:write` / `deploy:write`). Frontend guards `frontend/lib/rbac.ts`.

**Resource creation pattern:** UI form → typed API POST → `Operation` row → agent renders +
commits git → Argo/Portainer applies → status aggregated back into `resource_snapshots` →
UI reads snapshots. No direct cluster/docker access from browser.

**Entities that already exist** (models `backend/internal/models/`): `AppServer`, `Operation`,
`Project`, `Environment` (with `Runtime`), `User`, `Audit`, `AIModel`, `CloudTask`,
`DomainAuthorization`, `DomainHostname`, monitoring types. `App` is git-backed (rendered
manifest / compose), tracked via `resource_snapshots`, not a rich SQL row.

---

## 4. What already exists for VM import

| Capability | Status | Where | Reuse |
|---|---|---|---|
| VM / server entity | **Exists** | `backend/internal/models/appserver.go` (`AppServer`, status enum, `Source`=terraform/manual/beget-import); table `app_servers` migration `004_vm_track.sql`, `009_appserver_source.sql`, `012_appserver_import.sql` | **High** — reuse as-is |
| Manual VM connect (SSH) | **Exists** | payload `CreateAppServerPayload{Mode:"manual",VMIP,SSHUser,SSHPort,SSHPrivateKey}` `operation.go:78`; worker `doCreateManualAppServer` `portainer-agent/internal/worker/create_appserver.go:139`; UI `frontend/app/(console)/projects/[projectId]/app-servers/page.tsx` (manual mode form) | **High** |
| Terraform provision VM | **Exists** | `doCreateAppServer` `create_appserver.go:31`; `portainer-agent/internal/terraform/` | High (adjacent) |
| VM adopt (reverse-sync from Beget) | **Exists** | `beget_reader.go:64`, `terraform/adopt.go` (`import{}` + `ignore_changes=all`) | Medium — precedent for "import existing" |
| Agent registration / heartbeat | **Exists** | Portainer Edge Agent bootstrap `portainer-agent/internal/ssh/bootstrap.sh.tmpl`; `waitForAgent` + `IsAgentConnected` `portainer/client.go:146` | **High** |
| SSH (one-shot key) | **Exists** | `ssh/client.go:103 RunBootstrap`; key transits `operations.payload`, scrubbed on terminal state (`ScrubOperationSecret`) | High — but one-shot only, no persisted shell |
| Command execution on VM | **Missing** (only bootstrap stdin script) | — | — |
| Docker discovery (containers/images/volumes/networks/env) | **Missing as cloud verb**; **bash prototype** read-only | `scripts/vm-discover.sh` (SSH `docker inspect`/`ls`/`version`, emits `REPORT.md` + external-volume block) | Medium — logic is the spec; must be re-homed into agent |
| Container discovery (live, via cloud) | **Partial** | `portainer/client.go:333 ListContainers` (label filter) — lists only, no inspect detail | Medium |
| compose parsing (existing `compose.yaml` on disk) | **Missing** | — | — |
| File sync (remote file read/write) | **Missing** (git is the only file channel) | — | — |
| Logs | **Exists** | `client.go:346 StreamLogs`; backend `GetAppLogs`/`GetAppServer...`; UI `frontend/components/logs-viewer.tsx` | **High** |
| Metrics | **Exists** | bootstrap installs node_exporter + cAdvisor + Prometheus agent; backend `GetAppServerMetrics`/`GetAppMetrics`; UI `frontend/components/metrics/*` | **High** |
| Secrets / env management | **Partial** | `.env` in git plaintext (`gitops-agent/internal/renderer/renderer.go:317 RenderEnvFile`); env vars UI `frontend/components/deploy/env-vars-editor.tsx`; API `env/:key` CRUD | Medium — plaintext-in-git is the risk (§10) |
| Deployments | **Exists** | `DeployStack` op; deployments table + history (migration `013_git_build_deploy.sql`); UI deployments page | High (for image apps); compose deploy has no rich history |
| Rollback | **Partial** | `POST /deployments/:id/rollback` (`deploy:write`) — image/build deployments; **not compose stacks** | Low for compose |
| Git integration | **Exists** | `build-agent/internal/github/*`, `git_repos`/`git_installations` (migrations 013/023/026); UI `git/import` wizard | **High** |

**Verdict:** VM import *connect* is solid and shipped. VM import *discovery-and-adopt-existing-workload*
is the real hole — it lives as a bash script, not a product capability.

---

## 5. What already exists for Docker Compose management

| Capability | Status | Where |
|---|---|---|
| compose.yaml parsing | **Missing** (no structured parse; treated as opaque text/file) | — |
| compose generation (render) | **Partial** — skeleton only | `gitops-agent/internal/renderer/renderer.go:300 RenderComposeSkeleton` (minimal nginx); no full generation from spec or from discovery |
| compose deploy (up) | **Exists** | `portainer-agent/internal/worker/deploy_stack.go:27 doDeployStack` → `CreateStackFromGit` / `RedeployStack` (`client.go:180/190`); Portainer runs `docker compose up`, project name = stack name |
| pull / restart (via redeploy) | **Exists** | `RedeployStack{PullImage:true,Prune:false}` — release = bump image tag in git → redeploy |
| down / stop / remove | **Missing** | no `StopContainer`/`RemoveContainer`/`DeleteStack`-scoped-container verb; `DeleteStack` exists (`client.go:196`) but is whole-stack teardown |
| service-level ops (per compose service) | **Missing** | app = whole stack, no per-service control |
| volume management | **Partial (read)** | discovery bash emits external-volume block; no cloud volume CRUD |
| network management | **Partial (read)** | `docker network ls` in bash script; no cloud CRUD |
| env / secrets | **Partial** | `.env` two-pane editor + `RenderEnvFile`; plaintext-in-git |
| healthchecks | **Missing** (no surfaced health parse) | — |
| logs | **Exists** | `StreamLogs`, backend `GetAppLogs`, `state.go:195` container logs (tail) |
| shell / exec | **Missing** | — |
| image updates | **Exists** | `UpdateAppImage` (`PATCH .../apps/:appName/image`); redeploy pulls |
| registry auth | **Partial** | Nexus in build path (`build-agent/internal/registry/nexus.go`); compose git repo creds for Portainer; per-image private registry pull creds not surfaced |
| deployment history | **Partial** | image/build deployments have history; compose stack redeploys not richly versioned |

**Editor + live state (strong):**
- Two-pane editor UI `frontend/app/(console)/projects/[projectId]/apps/[appName]/compose/page.tsx`
  (compose.yaml + .env, two WS connections), reusable `frontend/components/ui/yaml-editor.tsx`
  (CodeMirror).
- WS file editor backend `gitops-agent/internal/server/ws_handler.go:76 handleFileWS`
  keyed by `wstoken.Claims.File` (`values.yaml` | `compose.yaml` | `.env`), token
  `backend/internal/wstoken/token.go`, API `backend/internal/api/apps_values.go`.
- Live state `backend/internal/api/state.go:127 GetAppState` filters
  `com.docker.compose.project=<app>`; container logs `state.go:195`; UI panel
  `frontend/components/compose-state-panel.tsx` (polls 10s).

**Verdict:** deploy-from-git + edit + observe is done. **Manage below stack level** (per-service,
per-container lifecycle, volumes/networks CRUD, exec) is missing — that is the "management"
that Coolify/Portainer users expect.

---

## 6. What already exists for GitOps

| Capability | Status | Where |
|---|---|---|
| GitHub / GitLab integration | **Exists** | `build-agent/internal/github/app.go` (App JWT, install tokens, repos, commit status); GitLab referenced in UI wizard |
| repo connection | **Exists** | `git_integrations` / `git_repos` (migr 013/023/026); API `ConnectGitRepo`; UI `git/import` |
| webhooks | **Exists** | `build-agent/internal/github/webhook.go` (HMAC verify); gitops-agent `/webhook/github` |
| commit status | **Exists** | `github/app.go PostStatus` → console build page |
| branch / env model | **Partial** | preview environments (migr 014) for k8s; VM env = single env; no branch→compose-env mapping |
| diff preview (UI shows change before apply) | **Missing** | editor commits directly; no plan/diff gate |
| desired vs actual (compose) | **Missing** | git desired exists; no compare against live `docker ps` |
| reconciliation loop (compose) | **Partial** | deploy is **event-driven** (commit → `DeployStack` op), NOT a continuous reconcile like Argo; no periodic converge for VM stacks |
| deployment history | **Partial** | image deploy history yes; compose stack redeploy history thin |
| rollback | **Partial** | image deployments rollback endpoint; **no compose stack rollback / revert-commit UI** |
| drift detection | **Missing** | nothing compares live containers to committed compose |
| git-watcher back-sync | **Exists** (for CRDs, not compose) | `gitwatcher.go` upserts `resource_snapshots` from manual commits; compose/app paths not part of snapshot back-sync |

**Verdict:** the *plumbing* of GitOps (git as source of truth, webhook, commit status,
operation-driven apply) is real. The *loop* semantics that sell "GitOps" — **diff → drift →
reconcile → rollback** — are absent for compose. Today it is closer to "git-triggered deploy"
than "GitOps reconcile."

---

## 7. Frontend/UI readiness

Route root: `frontend/app/(console)/`. All TypeScript + Tailwind + i18n + RBAC-guarded.

| Area | Status | Files |
|---|---|---|
| Projects (list/overview/switcher/create) | **Ready** | `projects/page.tsx`, `projects/[projectId]/page.tsx`, `components/shell/{project-switcher,create-project-modal,project-nav}.tsx` |
| Apps (list/detail/create/deploy) | **Ready** | `projects/[projectId]/apps/page.tsx`, `apps/[appName]/page.tsx`, `.../settings`, `.../deployments`, `.../builds/[buildId]` |
| Servers / VMs | **Ready** | `projects/[projectId]/app-servers/page.tsx` (manual + terraform modes), `app-servers/[serverName]/page.tsx` (state/metrics/logs/retry) |
| Compose / stack editor | **Ready** | `apps/[appName]/compose/page.tsx` (two-pane), `apps/[appName]/values/page.tsx`, reusable `components/ui/yaml-editor.tsx` |
| Live container state | **Ready** | `components/compose-state-panel.tsx` (container list + inline logs, 10s poll) |
| Deployments / history / rollback | **Ready (image)** / partial (compose) | `apps/[appName]/deployments/page.tsx`, `components/deploy/{build-log-viewer,build-status-badge}.tsx` |
| Logs viewer | **Ready** | `components/logs-viewer.tsx` (multi-source, search, time range, live) |
| Metrics / observability | **Ready** | `components/metrics/*` (drag/resize dashboard, KPI, explorer), `components/charts/*` (ECharts) |
| Secrets / env vars | **Ready** | `components/deploy/env-vars-editor.tsx` (mask/reveal, scope, permission-aware) |
| Git connections | **Ready** | `git/page.tsx`, `git/import/page.tsx` (install picker → repo → framework → config wizard) |
| Settings | **Ready** | `apps/[appName]/settings/page.tsx` (env / git / domains tabs) |
| Databases / storage / domains | **Ready** | `databases/`, `storage/`, `domains/` pages |

**Directly reusable for this feature:** `yaml-editor.tsx`, the `/ws/file` two-pane pattern,
`env-vars-editor.tsx`, `logs-viewer.tsx`, `compose-state-panel.tsx`, `build-log-viewer.tsx`,
metrics dashboard, `git/import` wizard shape, `app-servers` create modal.

**Missing UI:** import-existing-workload wizard (discover → review → generate compose →
commit), drift/diff view, per-service/per-container controls (start/stop/restart/exec),
compose deploy-history + revert.

---

## 8. Backend/API readiness

Router: `backend/internal/api/router.go`. Relevant existing endpoints:

| Need | Status | Endpoint / handler |
|---|---|---|
| VM list/create/get/delete | **Exists** | `GET/POST /projects/:p/app-servers`, `GET/DELETE .../:serverName`, handlers `appservers.go` |
| VM live state | **Exists** | `GET .../app-servers/:serverName/state` → `GetAppServerState` |
| VM metrics | **Exists** | `GET .../app-servers/:serverName/metrics` |
| Compose app create | **Exists** | `POST .../environments/:envId/apps` `CreateApp` (VM env → compose stack) `apps.go:104` |
| Compose editor token | **Exists** | `POST .../apps/:appName/values-token` `GetValuesToken` (accepts `file`) |
| Compose live state | **Exists** | `GET .../apps/:appName/state` `GetAppState` (`state.go:127`) |
| Compose logs | **Exists** | `GET .../apps/:appName/logs` `GetAppLogs` |
| Image update / deploy | **Exists** | `PATCH .../apps/:appName/image` `UpdateAppImage` |
| Env var CRUD | **Exists** | `GET/PUT/GET(reveal)/DELETE .../apps/:appName/env[/:key]` |
| Deployments + rollback/promote | **Exists (image)** | `GET .../apps/:appName/deployments`, `POST /deployments/:id/rollback|promote` |
| Git installs/repos | **Exists** | `.../git/installations*`, `.../repos*`, `DetectFramework` |
| Operations | **Exists** | `GET .../operations[/:id]`, `POST .../operations/:id/retry` |
| **Discovery API** (inventory a VM's docker) | **Missing** | — (only bash `scripts/vm-discover.sh`) |
| **Import compose API** (adopt existing stack → git) | **Missing** | — |
| **Container lifecycle API** (stop/remove/restart/exec) | **Missing** | — (Phase-4 decision) |
| **Drift / desired-vs-actual API** | **Missing** | — |
| **Compose stack rollback / revert-commit** | **Missing** | — |

**Operation actions already wired** (`operation.go` payload types + agent switches
`vm_watcher.go:77`, gitops `dbwatcher.go`): `CreateServiceDatabase`, `CreateS3Bucket`,
`CreateApp`, `DeployImageVersion`, `CreateAppServer` (terraform|manual), `DeleteAppServer`,
`UpdateAppEnvVars`, `CreatePublicApi`, `CreatePreviewEnv`/`DeletePreviewEnv`,
`AttachCustomHostname`/`DetachCustomHostname`, `DeployStack`, AI actions.

**New actions this feature needs:** `DiscoverVMWorkload`, `ImportComposeStack`,
`StopContainer`/`RemoveContainer` (or a single `AdoptVMWorkload` op), `DetectComposeDrift`.

---

## 9. Data model gaps

Current SQL rows are thin below "app-server" and "app-is-a-git-stack." For a real Compose
management + import product, propose (compare to current — most are **new**):

| Proposed entity | Current state | Note |
|---|---|---|
| `Server` / AppServer | **Exists** `app_servers` | keep; add `docker_version`, `discovered_at` maybe |
| `Agent` (edge agent registration) | **Implicit** (Portainer endpoint id on app_server) | no own row; probably fine while Portainer owns it |
| `ImportedStack` | **Missing** | a discovered compose project adopted from a VM (source, original path, adopt status) |
| `ComposeProject` | **Missing** (app≈stack, opaque) | first-class stack with services list |
| `ComposeService` | **Missing** | per-service row (image, ports, depends_on, health) — enables per-service UI |
| `ComposeVolume` | **Missing** (bash emits external block only) | needed for the data-safety + adopt story |
| `ComposeNetwork` | **Missing** | — |
| `GitOpsBinding` | **Partial** `git_integrations`/`git_repos` | exists for build repos; no explicit compose-app↔git-path↔branch binding as a row |
| `DeploymentRevision` (compose) | **Partial** `deployments` (image/build) | no compose stack revision history |
| `DriftReport` | **Missing** | live-vs-desired diff per stack |
| `RuntimeContainer` | **Missing** (queried live, not stored) | snapshot of `docker ps` for drift + UI |
| `DiscoveredResource` | **Missing** (bash `REPORT.md` only) | inventory record per discovery run |

**Reuse levers:** `Operation` + `resource_snapshots` already give async + status-aggregation
for free; new entities should ride those, not invent a parallel state store.

---

## 10. Risks and hard parts

1. **Agent security / root.** Bootstrap runs as root, installs Docker + sidecars
   (`bootstrap.sh.tmpl`). Any added *lifecycle* verbs (stop/remove/exec) hand the cloud
   real mutation power over customer prod. Must gate hard (RBAC `deploy:write`, audit,
   maybe approval for destructive ops). Exec-on-container is the biggest blast radius — treat
   as arbitrary code execution on the customer VM.
2. **Secrets / env leak.** `.env` is committed to git **in plaintext**
   (`renderer.go:317`, doc R1). For imported real prod stacks (fin-data DSNs, PG creds) this
   is a live exposure. Need either encrypted-at-rest secret path or explicit customer consent;
   §11 must call it out per stack.
3. **Arbitrary command execution.** Discovery today is a bash script with a read-only guard
   comment but no hard enforcement; re-homing into the agent must whitelist verbs
   (inspect/ls/version), never a generic "run this on the VM."
4. **PG volume data loss (documented, mitigated).** First `compose up` creates a fresh empty
   `<stack>_<vol>` and orphans prod data. Mitigation exists (`external: true` + literal name
   from `vm-discover.sh`) but depends on discovery being run and pasted — automate it into the
   import flow or it *will* be forgotten.
5. **Two postgres on one datadir during cutover → corruption.** Old hand-run container must be
   stopped *before* stack deploy. Blocked on the missing stop verb (Phase-4 decision).
6. **Rollback of compose projects.** No revert story; a bad `compose.yaml` commit redeploys a
   broken stack. `Prune:false` + external volumes limit damage but there is no one-click revert.
7. **Idempotency.** Deploy is idempotent-ish (redeploy re-pulls); import/adopt is not designed
   yet — re-running import must not double-create or clobber.
8. **Drift.** No detection: UI-committed compose can silently diverge from a VM someone
   `docker`-edited by hand. This is the classic GitOps trust problem and is entirely unbuilt.
9. **Runtime containers ≠ compose file.** An imported VM's `docker ps` rarely maps cleanly to
   one compose project (mixed hand-run + compose + orphans). Reverse-generation is lossy;
   must flag unmapped containers rather than pretend a clean import.
10. **Networking / volumes / bind mounts.** Bind mounts (vs named volumes) need host-path
    mirroring; discovery flags them but authoring is manual.
11. **Remote file management.** No channel to read/write arbitrary files on the VM except the
    one-shot bootstrap; git is the only file plane. Fine for compose, blocks anything needing
    host files.
12. **Multi-server.** One app = one endpoint. No stack spread / scheduling across VMs.
13. **UI vs Git conflict.** WS editor commits directly (LWW), and a manual git commit or a
    hand-`docker` change can conflict; no diff/approval gate today.

---

## 11. Recommended MVP (2–4 weeks)

**Theme: "Adopt an existing Compose VM into Dada, safely, and run releases from git."**
Lean entirely on what exists; close only the smallest gap that unlocks the story.

**Include:**
- **Import wizard v1** = re-home `scripts/vm-discover.sh` logic behind a new read-only
  `DiscoverVMWorkload` operation (run over the *already-connected* edge agent's Portainer
  proxy where possible; SSH read-only fallback). Output: inventory + auto-pinned external
  volume block. UI: a review screen (reuse `git/import` wizard shape + `compose-state-panel`).
- **Generate + commit compose** from the discovery (skeleton generator upgraded from
  `RenderComposeSkeleton` to emit the discovered images/ports/volumes-external/.env). Human
  reviews in the existing two-pane editor before first deploy.
- **Deploy from git** — already works (`doDeployStack`). Release = bump tag in git → redeploy.
- **Live state + logs + metrics** — already works, wire into the app detail page.
- **One new lifecycle verb: `StopContainer`** (Phase-4 decision A, minimal) so cutover can
  retire hand-run containers through the cloud instead of a runbook. Gate on `deploy:write` +
  audit. (If time-boxed: ship decision **B** — operator stops in Portainer UI — and defer A.)
- **Secret consent gate**: on import, explicitly warn `.env` lands in git plaintext; require
  acknowledgement (addresses risk #2 minimally).

**Explicitly exclude from MVP:** drift detection, compose rollback/revert UI, per-service
controls, exec/shell, multi-server, network/volume CRUD, reverse GitOps automation beyond the
one-shot import, Coolify/Portainer importers.

**APIs added:** `DiscoverVMWorkload` (op), `ImportComposeStack` (op, render+commit), optional
`StopContainer` (op). Everything else reuses existing endpoints.

**Screens added:** import/adopt wizard (discover → review → generate → commit → deploy). Reuse
everything else.

**Entities added:** minimal — a `DiscoveredResource`/inventory record + `ImportedStack` status;
avoid the full compose-service/volume/network model in MVP.

**Constraints:** read-only discovery only; no exec; `Prune:false` hard; external volumes
mandatory; destructive verbs behind RBAC + audit.

---

## 12. Recommended v2 / v3

- **Reverse GitOps proper:** continuous `docker ps` → desired compose diff → **DriftReport** →
  one-click reconcile or accept-into-git. Needs `RuntimeContainer` snapshots + `DriftReport`
  entity + a periodic reconcile loop for VM stacks (today deploy is event-only).
- **Per-service / per-container management:** start/stop/restart/exec (behind approval),
  requires `ComposeService`/`RuntimeContainer` model + new agent verbs.
- **Compose rollback / revert-commit UI:** `DeploymentRevision` for stacks + git revert action.
- **Importers:** Coolify / Portainer / Komodo config import (read their compose + env, map into
  Dada stacks). Strong wedge for the target audience — but see §14 positioning.
- **Multi-server:** stack spread / scheduling across endpoints; edge stacks
  (`CreateEdgeStackFromGit` already exists) as the substrate.
- **Swarm / k8s migration path:** "graduate" a compose app to the k8s runtime (compose→helm),
  the natural "from VM to full Dada Cloud" story the feature brief wants.
- **Dada Stack abstraction:** opinionated bundle (app + Postgres + Redis + domain + TLS + backups)
  deployable to a VM the same way as k8s — unifies the two runtimes under one product noun.
- **Managed Postgres/Redis/S3/DNS/SSL on VMs:** extend the existing k8s managed-service actions
  to the VM runtime.
- **Secret hardening:** encrypted secret channel instead of plaintext `.env` in git.
- **AI RCA on logs/metrics:** reuse `dadagent` cloud-task + monitoring read layer.
- **Marketplace templates:** opinionated compose stacks (matches vision "Templates" MVP item).

---

## 13. File map

| Area | Existing files | Notes | Reuse potential |
|---|---|---|---|
| VM model / status | `backend/internal/models/appserver.go`; migr `004_vm_track.sql`, `009_appserver_source.sql`, `012_appserver_import.sql` | AppServer + source (terraform/manual/beget-import) | **High** |
| Operation model / payloads | `backend/internal/models/operation.go` | typed actions incl. manual VM, DeployStack | **High** |
| Manual VM connect worker | `portainer-agent/internal/worker/create_appserver.go` (`doCreateManualAppServer`) | SSH one-shot bootstrap | **High** |
| VM adopt (import precedent) | `portainer-agent/internal/worker/beget_reader.go`, `internal/terraform/adopt.go` | `import{}` + freeze | Medium |
| Portainer client | `portainer-agent/internal/portainer/client.go` | CreateStackFromGit/RedeployStack/ListContainers/StreamLogs; **no stop/remove** | High (extend) |
| SSH bootstrap | `portainer-agent/internal/ssh/{client.go,bootstrap.sh.tmpl}` | root install Docker + sidecars + edge agent | High |
| Compose deploy | `portainer-agent/internal/worker/deploy_stack.go` | git → Portainer up/redeploy | **High** |
| Git render / paths / .env | `gitops-agent/internal/renderer/renderer.go` (`AppComposeGitPath`,`AppEnvGitPath`,`RenderComposeSkeleton`,`RenderEnvFile`) | skeleton only; plaintext .env | **High** (extend generator) |
| Git watcher / snapshots | `gitops-agent/internal/worker/{gitwatcher.go,discovery.go}` | back-sync CRDs; compose not synced | Medium |
| WS file editor | `gitops-agent/internal/server/ws_handler.go`; `backend/internal/wstoken/token.go`; `backend/internal/api/apps_values.go` | values/compose/.env by File claim | **High** |
| Live state / logs API | `backend/internal/api/state.go`; `backend/internal/portainer/client.go` | read-only Portainer proxy | **High** |
| Discovery prototype | `scripts/vm-discover.sh` | read-only docker inventory + external-volume block | **High** (logic → agent) |
| VM→GitOps plan | `tasks/vm-gitops-migration-plan.md` | 6-phase cutover, Phase-4 decision | reference |
| Design doc | `docs/architecture/compose-and-manual-vm-design.md` | the approved spec | reference |
| Runtime ADR | `docs/adr/ADR-007-portainer-edge-agent-runtime.md` | Portainer chosen, Coolify rejected | reference |
| Product vision | `docs/product/product-gtm-vision.md` | outcomes-not-infra positioning | reference (constrains §14) |
| Frontend compose editor | `frontend/app/(console)/projects/[projectId]/apps/[appName]/compose/page.tsx`; `frontend/components/ui/yaml-editor.tsx` | two-pane WS | **High** |
| Frontend app-servers | `frontend/app/(console)/projects/[projectId]/app-servers/*` | manual+terraform modes | **High** |
| Frontend live state | `frontend/components/compose-state-panel.tsx` | container list + logs | **High** |
| Frontend logs/metrics/env/git | `frontend/components/{logs-viewer,metrics/*,deploy/env-vars-editor}.tsx`; `frontend/app/(console)/projects/[projectId]/git/import/page.tsx` | reusable | **High** |
| Build / GitHub | `build-agent/internal/github/*`, `internal/detect/detect.go`, `internal/worker/runner.go` | GitHub App, webhook, detect, Jenkins | High (adjacent) |

---

## 14. Final recommendation

**Should we build it? Yes — because most of it is already built.** The incremental cost to
get a genuine "adopt an existing Docker Compose VM and run it GitOps-style" flow is small:
one import wizard, an upgraded compose generator, and one (maybe zero) new lifecycle verb.
Everything else — connect, deploy, edit, observe — ships today. This is the highest
leverage-per-effort feature available.

**Positioning — the real decision.** The brief says "grab the Coolify / Portainer / Komodo
audience, but not as another Coolify — as the path from a VM to full Dada Cloud." The canonical
vision (`product-gtm-vision.md`) forbids selling infra/GitOps/Kubernetes in the hero. **Both
can be true if you frame it as a migration on-ramp, not a category:**
- Landing hero stays outcomes ("GitHub → backend → Postgres → domain → HTTPS → rollback").
- Add a dedicated **"Bring your VPS / Docker Compose"** migration angle (the vision doc itself
  lists this as a growth experiment: *"Offer free VPS migration"*, *"Compare page: VPS vs …"*).
  The wedge is *"stop SSH-releasing; keep your compose, get git releases + logs + rollback"* —
  then upsell into managed DB/domains/k8s = "full Dada Cloud."
- Do **not** market a Portainer/Coolify feature-parity dashboard; ADR-007 deliberately declined
  to be that. Dada wins on the *path off* the VM, not on being a nicer Portainer.

**What to do first (order):**
1. Decide positioning (migration on-ramp, not "Coolify clone"). One-page GTM note; align hero
   copy owner.
2. Re-home `vm-discover.sh` into a read-only `DiscoverVMWorkload` agent op + review UI.
3. Upgrade the compose generator to emit discovered images/ports/**external volumes**/.env.
4. Ship the import → review → commit → deploy wizard reusing existing editor + state panels.
5. Add `StopContainer` (decision A) *or* accept decision B (Portainer-UI stop) for fin-data
   first-run; make the cutover reproducible.

**Technical decisions to make:**
- Adopt Phase-4 **decision A** (cloud stop/remove verb) as the long-term path; B only as
  interim. Don't ship exec in MVP.
- Keep git as source of truth; add **drift detection in v2**, not MVP — but design the
  `RuntimeContainer` snapshot now so drift is cheap later.
- Fix the secret story before importing real customer prod at scale: plaintext `.env` in git
  is acceptable for the platform's own convention but is a liability for imported third-party
  prod DSNs. At minimum gate with explicit consent in MVP; encrypted channel in v2.

**What NOT to do in MVP:**
- No exec/shell on VMs.
- No per-service/per-container control panel (that's "being Portainer" — resist).
- No drift/reconcile loop yet (design the schema, ship the loop in v2).
- No multi-server stack spread.
- No automatic Coolify/Portainer importer (v2 wedge, after the native import proves out).
- No compose→helm migration in MVP (it's the v2/v3 "full Dada Cloud" story).

**Uncertain / verify before building:**
- Whether Portainer's docker proxy can enumerate enough (image/volume/network inspect, not
  just `ListContainers`) to do discovery **without** SSH — verify against a live edge endpoint;
  if not, discovery keeps a read-only SSH leg (uncertain).
- Whether `resource_snapshots` back-sync can be extended to compose stacks cleanly, or whether
  compose needs its own snapshot table (uncertain — check `gitwatcher.go` path regexes).
- Exact private-registry pull-cred path for compose images on a customer VM (uncertain).
