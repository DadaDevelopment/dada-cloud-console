# Vercel-flow — Full Implementation Plan (production, not MVP)

Status: plan (ready to execute)
Date: 2026-06-20
Companion: tasks/vercel-flow-design.md (design), docs/architecture/vercel-flow.svg (diagram)
Custom-domain+SSL: separate task (chip task_fcd575ca) → tasks/custom-domain-ssl-design.md

Synthesis of 4 codebase-aware design passes (build engine / registry+git+env / api+data+deploy+preview / frontend).

---

## 0. Architectural invariants (do not violate)

1. **Build is imperative, NOT gitops.** Builds live in the `builds` table + k8s Jobs, owned
   by a new `build-agent`. They NEVER write to `operations`. Only the *result* (an immutable
   image tag) re-enters the declarative path via the EXISTING `DeployImageVersion` operation.
2. **gitops-agent claims by DENYLIST** (`gitops-agent/internal/db/operations.go:47`,
   `action NOT IN (...)`). Therefore any build-related action left in `operations` would be
   wrongly claimed. Keep build lifecycle out of `operations`. New declarative actions
   (`CreatePreviewEnv`/`DeletePreviewEnv`) are auto-claimed — must NOT be added to denylist.
3. **build-agent NEVER touches Argo/Helm/k8s workloads.** Its success path only does DB writes
   + enqueues `DeployImageVersion`. The unchanged rails (gitops-agent → Argo → pod) do the deploy.
4. **Deploy = `DeployImageVersionPayload{AppName, Image}`** reused for fresh deploy, rollback,
   promote, preview-deploy. No new deploy action.
5. **Reuse existing primitives unchanged**: Operation machine, gitops renderer/worker, ArgoCD,
   Crossplane App, PublicApi, cert-manager, ingress-nginx-pub, Keycloak, `wstoken` WS hub,
   `project_members`/`auth.Claims`, `project_quotas`, multi-env-per-project.

---

## 1. CRITICAL PREREQUISITE — encrypt path (P0, blocks everything with secrets)

Backend crypto is **decrypt-only today**. `gitops-agent/internal/crypto/crypto.go:13`
(`DecryptToken`) is the only production crypto; encrypt exists only as a test helper
(`crypto_test.go:15`). `git_integrations` rows are seeded externally.

- New `backend/internal/crypto/crypto.go` with `EncryptToken(keyHex, plaintext) ([]byte, error)`.
  Format fixed by decryptor (`crypto.go:34-43`): `nonce(12) || aes-256-gcm(plaintext)`,
  key = hex 32 bytes from `GITOPS_ENCRYPTION_KEY`.
- Wire `GITOPS_ENCRYPTION_KEY` into `backend/internal/config` (same secret value as
  gitops-agent, `helm/dada-cloud-console/values.yaml:126`).
- build-agent imports existing `gitops-agent/internal/crypto` for decrypt + needs same key.

**Blocks**: GitHub-App token storage, env-var storage, registry robot-secret storage. DO FIRST.

---

## 2. Infrastructure prerequisites (parallel, block the agent)

| Item | What | Notes |
|---|---|---|
| **Harbor** | helm install as separate ArgoCD app in argo-infra (`apps/harbor/`), ns `harbor`, `harbor.dada-tuda.ru`, cert via existing `letsencrypt-prod` ClusterIssuer | Trivy on, jobservice on, persistence PVCs. Zot = downgrade only if single-tenant. |
| **BuildKit builder image** | our `BUILDER_IMAGE` = git + nixpacks + buildctl + entrypoint script | published to Harbor |
| **Build node pool** | dedicated tainted pool `dada.io/pool=build` + toleration | isolation |
| **gVisor/Kata** | `runtimeClassName: gvisor` (`BUILD_RUNTIME_CLASS`) | sandbox untrusted code |
| **CNI NetworkPolicy** | Cilium/Calico must enforce NetworkPolicy | needed for egress lockdown |
| **build-agent k8s SA** | scoped: CRUD Jobs/Secrets/NetworkPolicies/Namespaces with `dada.io/build` label only — NEVER workloads | RBAC in helm |
| **GitHub App** | register App, permissions below, webhook → build-agent | external one-time setup |

GitHub App permissions (least privilege): Contents R, Metadata R, Commit statuses R/W,
Checks R/W, Pull requests R, Webhooks. Events: push, pull_request.

---

## 3. Data model — 2 migrations

### `backend/migrations/013_git_build_deploy.sql`
Tables: `git_repos`, `git_app_installations`, `builds`, `builds_logs`, `deployments`, `env_vars`.
Conventions: numbered, forward-only, idempotent (`IF NOT EXISTS`), `GRANT ... TO dada;` footer
(see `006_ai_studio.sql:78`). Key constraints:
- `builds`: `UNIQUE(git_repo_id, commit_sha)` (webhook idempotency); status enum
  `queued|detecting|building|pushing|success|failed|canceled`; partial index on `status='queued'`.
- `deployments`: `image_uri` denormalized (immutable); `operation_id` = ONLY link to operations;
  partial unique index `idx_deployments_current ON (environment_id, app_name) WHERE is_current`.
- `env_vars`: `value_encrypted BYTEA` (AES-GCM, §1); `is_secret`; `scope IN (build|runtime|both)`;
  `UNIQUE(environment_id, app_name, key)`.
- `git_repos`: per-(project,env,app) unique; `token_encrypted BYTEA`; `webhook_secret`;
  `production_branch`, `root_dir`, `framework_override`, `auto_deploy`.
- GitHub-App tokens are NOT stored (minted per-build, ~1h). GitLab uses `token_encrypted`.

### `backend/migrations/014_preview_environments.sql`
Preview envs = ephemeral rows in EXISTING `environments` table (zero new gitops code).
- `ALTER TABLE environments ADD`: `is_ephemeral`, `git_repo_id`, `pr_number`, `pr_head_branch`,
  `parent_env_id`, `expires_at`.
- Relax `environments_type_check` to admit `'preview'`.
- `idx_environments_preview_pr UNIQUE (git_repo_id, pr_number) WHERE is_ephemeral`.
- `project_quotas ADD preview_env_max INT DEFAULT 5`.

---

## 4. build-agent (new Go module — the core engine)

Sibling to gitops-agent/portainer-agent. Mirror their conventions exactly
(`cmd/<agent>/main.go` → `config.Load()` → `db.Connect()` → workers `go Start(ctx)` →
`signal.NotifyContext`; flat Config from env; poll loop with `FOR UPDATE SKIP LOCKED`).

### Package layout
```
build-agent/
  cmd/build-agent/main.go
  internal/
    config/      Config + Load() (BUILD_* env)
    db/          pool(copy) builds repos deploy logs crypto(copy)
    queue/       scheduler.go  (semaphore, per-build ctx, supersede)
    worker/      poller.go (poll-fallback)  runner.go (drives one build)
    detect/      nixpacks.go  dockerfile.go
    builder/     job.go (k8s Job)  template.go (go:embed YAML)  logs.go
    registry/    harbor.go (ensure project/repo, robot mint)
    isolation/   namespace.go  netpol.go
    github/      app.go (JWT→install token, commit status)  webhook.go (HMAC)
    server/      server.go (/healthz /webhook/github /ws/build)  hub.go(copy)  ws_handler.go
    wstoken/     token.go (copy; Claims += BuildID)
    metrics/     metrics.go (Prometheus)
```

### Triggers (two, like gitops-agent)
- **Webhook = nudge**: `POST /webhook/github`, HMAC `X-Hub-Signature-256` verify (copy
  `gitops-agent/internal/server/server.go:137-149`), resolve `git_repos` by `repo_full_name`,
  verify against that repo's `webhook_secret`, map branch→env, idempotent
  `INSERT builds ... ON CONFLICT (git_repo_id, commit_sha) DO NOTHING`. Return 200 fast.
- **Poller = backstop**: ticker (`BUILD_POLL_INTERVAL=5s`), `ClaimQueued` via SKIP LOCKED.
  Catches builds enqueued while webhook server down + manual/rollback triggers from backend.

### Build state machine
`queued → detecting → building → pushing → success` (+ `failed`/`canceled` terminal).
Each transition = compare-and-set `UPDATE ... WHERE id=$1 AND status=$prev`.
- **Supersession (Vercel behavior)**: newer commit on same `repo+branch` cancels older
  in-flight builds (in-proc scheduler signal + SQL guard on claim).
- **Concurrency**: buffered semaphore `BUILD_MAX_CONCURRENT=4` + namespace ResourceQuota.
- **Timeout**: `context.WithTimeout(BUILD_TIMEOUT=20m)` + Job `activeDeadlineSeconds` mirror.
- **Retry**: only INFRA failures retry (Job-create reject, push 5xx) — bounded backoff,
  `BUILD_MAX_RETRIES=2`. User-code build failures NEVER retry.
- **Crash recovery**: poller reconciles non-terminal builds with missing/finished Jobs.

### Framework detect + build (inside the Job, not the agent)
Untrusted source must not enter the control-plane. Job entrypoint runs:
1. `git clone --depth 1 --branch <b> <url>` (askpass via mounted creds) → `checkout <sha>`.
2. If `<root_dir>/Dockerfile` exists → BuildKit dockerfile frontend. Else
   `nixpacks build <root_dir> --out .nixpacks` (generates Dockerfile+assets) for zero-config.
3. `buildctl build --frontend dockerfile.v0 --local context=<root> --local dockerfile=<dir>
   --output type=image,name=<img>,push=true --export-cache type=registry,ref=<cacheTag>,mode=max
   --import-cache type=registry,ref=<cacheTag>` + `--secret` mounts for sensitive vars.
4. Print pushed digest → agent pins `builds.image_uri = harbor.../<proj>/<app>@sha256:<digest>`.
- `framework_override`, `root_dir` from `git_repos`. Build-time env via `--build-arg`
  (non-sensitive) / `--secret` (sensitive — never baked into layers).

### Image builder = BuildKit per-Job (NOT shared daemon)
Per-Job ephemeral rootless `buildkitd` sidecar (cross-tenant isolation; shared daemon = blast
radius). Cache shared safely via **registry-backed cache** (one cache tag per repo), not a
shared daemon. PVC = documented perf-only fallback.

### Multi-tenant isolation (security spine — the hard part)
Layered, set up by runner before Job, torn down after:
1. `runtimeClassName: gvisor` + dedicated tainted build node pool.
2. Per-build ephemeral namespace `build-<id>` (+ ResourceQuota + LimitRange), deleted on finish.
3. NetworkPolicy default-deny; egress allowed ONLY to Harbor + git host CIDRs
   (`BUILD_GIT_EGRESS_CIDRS`) + kube-dns.
4. `automountServiceAccountToken: false` + SA `build-runner-noperms` bound to ZERO roles.
5. seccomp RuntimeDefault, drop ALL caps, readOnlyRootFilesystem on builder, runAsNonRoot.
   (buildkitd needs seccomp Unconfined — gVisor + tainted pool contain it.)
6. CPU/mem/pids/ephemeral-storage quotas + emptyDir sizeLimit.
7. Per-build short-lived secrets in the ephemeral ns: `-git` (1h GitHub install token / GitLab
   PAT), `-registry` (Harbor robot), `-buildenv` (app build env). Deleted with ns.
8. Secret scrubbing: sensitive via `--secret` not build-arg; log redaction pass; no secrets in
   `builds.summary` or Prometheus labels.

### Log streaming (reuse WS hub verbatim)
- Capture k8s `GetLogs(...).Stream` → redact → (a) `builds_logs` table (live/recent),
  (b) fan-out to hub (key `build/<id>`). On terminal: gzip → object store (S3Bucket infra),
  set `builds.logs_ref`, prune `builds_logs`.
- `/ws/build`: copy `gitops-agent/internal/server/hub.go` + `ws_handler.go`; replay backlog
  then live frames. Auth via `wstoken` with new `BuildID` claim; backend mints token
  (copy `backend/internal/api/apps_values.go:107-121`). New secret `BUILD_AGENT_TOKEN_SECRET`.

### Deploy handoff (the elegant seam)
On `pushing → success`, runner does ONLY DB writes:
1. pin `builds.image_uri = ...@sha256:<digest>` (+ human `:<gitsha>` tag).
2. `INSERT deployments` (not yet current).
3. `INSERT operations (... 'DeployImageVersion' ... 'Created')` with
   `DeployImageVersionPayload{AppName, Image}`, `actor_id` = system user (`010_system_user.sql`),
   write returned op id back to `deployments.operation_id`.
4. Existing rails run: gitops claims (denylist OK) → `doDeployImageVersion`
   (`dbwatcher.go:611`) → render + commit → ArgoCD sync → pod.
5. **`is_current` flip on op `Ready`**: watcher (poll `operations.status` for
   `deployments.operation_id`) runs tx {clear old current; set this}. Partial unique index
   guarantees single winner.
6. First successful deploy also enqueues `CreatePublicApi` (existing) →
   `<app>-<sha8>.apps.dada-tuda.ru` + `<app>-git-<branch>...` → cert-manager LE cert.

### Config env vars
`DATABASE_URL`, `BUILD_WEBHOOK_PORT=8091`, `BUILD_POLL_INTERVAL=5s`, `BUILD_MAX_CONCURRENT=4`,
`BUILD_TIMEOUT=20m`, `BUILD_MAX_RETRIES=2`, `BUILD_RUNTIME_CLASS=gvisor`, `BUILD_NODE_POOL_LABEL`,
`BUILD_CPU_LIMIT/MEM_LIMIT`, `BUILD_GIT_EGRESS_CIDRS`, `HARBOR_URL/ADMIN_USER/ADMIN_SECRET`,
`BUILDER_IMAGE`, `BUILD_GITHUB_APP_ID/APP_KEY/WEBHOOK_SECRET`, `GITOPS_ENCRYPTION_KEY`,
`BUILD_AGENT_TOKEN_SECRET`, `BUILD_LOG_OBJECT_STORE_URL`, `BUILD_LOG_DB_RETENTION`, in-cluster kube.

### Metrics
`build_total{result}`, `build_duration_seconds{phase}`, `build_queue_depth`, `builds_inflight`,
`build_cache_hit_total`, `build_superseded_total`, `build_retry_total`. Labels: project/app only.

---

## 5. Registry + Git + Env-vars details

### Harbor
- Per-dada-project Harbor project (slug = `projects.name`), created lazily on first repo link.
- Two robot accounts per project: `+build` (push+pull → BuildKit), `+deploy` (pull-only →
  imagePullSecret per env namespace, patched onto default SA — like global
  `imagePullSecrets: github-container-registry` at `values.yaml:3` but namespace-scoped).
- Quota per project; retention (keep last N + untagged>7d) + weekly GC; tag-immutability rule
  on `**` (enforces tag=sha); Trivy scan-on-push, optional block-pull on High CVE.

### GitHub App / GitLab
- Install flow: `github.com/apps/<app>/installations/new?state=<projectId+csrf>` → callback
  stores `git_app_installations` row bound to project.
- List repos: `GET /installation/repositories`. Clone: App JWT → install token (~1h) →
  `git clone https://x-access-token:<tok>@github.com/<full>.git`.
- Webhook: push → enqueue build (branch→env); pull_request opened/synchronize → preview build;
  closed → teardown. **Fork-PR safety**: `head.repo != base` → `fork_unsafe` flag → inject NO
  secrets into that build (Vercel parity).
- Commit status/checks back on each transition with details URL → console build page.
- GitLab: no App; encrypted project token + `X-Gitlab-Token` webhook (constant-time compare).

### Env vars
- `scope`: build → BuildKit only; runtime → app `.env`/values; both.
- sensitive: encrypted always; API returns masked, reveal-on-demand (copy
  `aiModelsApi.revealApiKey`, `api.ts:332`). Limits: ≤4KiB/var, ≤64KiB/app-target.
- **Runtime injection is a real gap** — today `AppValuesSpec`
  (`gitops-agent/internal/renderer/renderer.go:134-139`) has no env field:
  1. add `Env map[string]string` + sensitive-Secret ref to `AppValuesSpec`/`AppSpec`.
  2. `RenderAppValues` emits `env:` (non-sensitive) + references a per-app k8s Secret for
     sensitive (do NOT commit plaintext secrets to argo-infra git — apply Secret directly to
     env ns like imagePullSecret; chart `envFrom` it).
  3. **Resolve env_vars in gitops-agent at render time** (decrypt with its key) rather than
     putting them in `operations.payload` (which is plaintext).
- VM/compose track: mechanism exists (`RenderEnvSkeleton` writes `.env`,
  `UpdateAppEnvVarsPayload` exists). Same sensitive-out-of-band caveat.

---

## 6. Backend API (handlers, match router.go + apps.go patterns)

Auth on every handler: `auth.GetClaims` → parse params → `getUserProjectRole(...)`
(`ErrNoRows`⇒404) → writes `canWrite(role)`⇒403. Reads `200 {plural:[...]}`; deploys
`202 {operation, message}`; build triggers `202 {build}` (no operation — imperative).

New files + routes (`backend/internal/api/router.go` authed group):
```
gitrepos.go    GET/POST/DELETE .../environments/:envId/repos ; GET .../git/installations/:id/repos
builds.go      GET/POST .../apps/:app/builds ; GET .../builds/:id ;
               POST .../builds/:id/cancel ; POST .../builds/:id/logs-token (WS delegate)
deployments.go GET .../apps/:app/deployments ;
               POST .../deployments/:id/rollback ; POST .../deployments/:id/promote
envvars.go     GET .../apps/:app/env ; PUT .../env/:key ; DELETE .../env/:key
```
- Webhook `POST /api/v1/webhooks/github` registered OUTSIDE auth group, owned by build-agent.
- New handler field `h.buildagent` (base URL + WS-token secret), gated like `h.portainer`.
- `wstoken.Claims` gains `Build string`.
- Rollback/promote: read prior `deployments.image_uri` (immutable) → new `deployments` row
  (`trigger=rollback|promote`) → enqueue `DeployImageVersion` (same path). Seconds, no rebuild.

New operation payloads (`backend/internal/models/operation.go`): `CreatePreviewEnvPayload`,
`DeletePreviewEnvPayload` ONLY. Reuse `DeployImageVersionPayload`, `CreatePublicApiPayload`,
`DeleteAppPayload` unchanged. New gitops worker cases `doCreatePreviewEnv`/`doDeletePreviewEnv`
in `dbwatcher.go:180` switch (auto-claimed; NOT in denylist).

---

## 7. Preview environments (Vercel parity)

Preview env = `environments` row `is_ephemeral=TRUE`, own namespace. All apps/deploy/domain
code works unchanged.
- **PR opened**: quota check vs `preview_env_max` → `CreatePreviewEnv` op (gitops inserts env
  row, renders ns + ResourceQuota via `namespace_policy.go`, copies env_vars from `parent_env_id`)
  → build (`trigger=pr`) → `DeployImageVersion` into preview env → `CreatePublicApi`
  `<app>-git-<branch>.apps.dada-tuda.ru` → PR comment with URL.
- **PR synchronize**: new build → same preview env → `is_current` flips on Ready.
- **PR closed**: `DeletePreviewEnv` → per-app `DeleteApp` clean-prune (ADR-0005,
  `dbwatcher.go:476`) + `DeletePublicApi` → remove ns folder → `DELETE environments`
  (cascades env_vars/deployments/builds).
- **TTL reaper**: cron enqueues `DeletePreviewEnv` for `expires_at < NOW()`.

---

## 8. Frontend console

- `lib/resources.ts`: add nav `deployments` ("Deployments"), `git` ("Git & Builds"); icons
  `git/deployments/domains` in `components/shell/icons.tsx`.
- `lib/types.ts`: `Build`, `Deployment`, `GitRepo`, `EnvVar`, `AppDomain`, `GitInstallation`,
  `GitRemoteRepo`, `FrameworkDetection`, `BuildLogFrame` + `*Response` wrappers.
- `lib/api.ts`: `gitApi`, `buildsApi`, `deploymentsApi`, `envVarsApi`, `appDomainsApi`.
  Build/deploy/rollback return `OperationResponse` → reuse operations-polling redirect.
- New pages under `app/(console)/projects/[projectId]/`:
  - `git/page.tsx` (repos hub) + `git/import/page.tsx` (3-step wizard: connect → pick → configure
    with Nixpacks detection + editable build settings).
  - `deployments/page.tsx` (project-wide) + `apps/[appName]/deployments/page.tsx` +
    `.../deployments/[deploymentId]/page.tsx` (per-commit detail).
  - `apps/[appName]/builds/[buildId]/page.tsx` (live log viewer).
  - `apps/[appName]/settings/page.tsx` (tabs: Git / Env Vars / Domains).
- Components `components/deploy/`: `build-status-badge`, `build-log-viewer` (port values-editor
  WS lifecycle `values/page.tsx:90-183`), `deployment-row`, `framework-card`, `repo-picker`,
  `env-vars-editor`, `domains-panel`, `promote-modal`.
- Poll every 3s while any build in-progress (copy `operations/page.tsx:111`).
- Reveal-secret UX copies `aiModelsApi.revealApiKey`. Domains panel stubs link to custom-domain
  wizard (separate task).

---

## 9. Dependency graph / sequencing

```
P0  encrypt path (§1) ──────────────┐
                                     ├─► everything storing secrets
P0  migration 013/014 (§3) ─────────┤
                                     │
INFRA Harbor (§2) ─┐                 │
INFRA BuildKit img │                 │
INFRA node pool +  ├─► build-agent core (§4) ◄── GitHub App (§2/§5)
      gVisor + CNI │         │
INFRA build SA ────┘         │ enqueues DeployImageVersion
                             ▼
                   EXISTING rails (gitops→Argo→pod) — zero new work
                             │
renderer env support (§5) ───┘ (parallel; blocks runtime env injection)
                             │
backend API (§6) ── frontend (§8)   [read/trigger endpoints can ship vs schema early]
                             │
preview envs (§7) ── needs build-agent + GitHub App + 014
                             │
custom domain+SSL ── SEPARATE TASK (chip task_fcd575ca)
```

## 10. Phasing

1. **Close the loop**: §1 encrypt, 013, Harbor+BuildKit+isolation, build-agent core +
   webhook, GitHub App, deploy handoff via existing `DeployImageVersion`, auto temp domain.
   Prod branch only.
2. **Logs + PR status**: `builds.go` list/get/cancel + WS logs-token; commit-status to GitHub.
3. **Rollback + env vars**: `deployments` + `is_current` flip + rollback/promote; `envvars.go`
   + renderer env injection.
4. **Preview envs**: 014, `CreatePreviewEnv`/`DeletePreviewEnv`, PR open/sync/close + TTL + quota.
5. **Custom domain + SSL**: separate task (engine = cert-manager already exists).

## 11. Hard parts (ranked)

1. **Multi-tenant build isolation** (§4 isolation) — untrusted code in cluster. The real
   security work. gVisor/Kata + node pool + NetworkPolicy + no SA token + ephemeral ns + quotas.
2. **Runtime env-var injection** — renderer has no env field today; sensitive vars must not hit
   git plaintext (apply as k8s Secret directly to env ns).
3. **Encrypt path** — must build before any secret storage (easy but blocking).
4. **`is_current` flip correctness** under concurrent deploys — partial unique index + tx ordering.
5. NOT attempted: Vercel's ms global routing layer. k8s rollout in seconds is fine.
```
