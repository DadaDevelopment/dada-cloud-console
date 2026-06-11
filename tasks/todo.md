# 2026-06-11 Reflective MCP Server

Design: `tasks/mcp-server-design.md`. Shippable milestones, lowest-risk first.

## M0 — De-risk spike ✅
- [x] R5: `modelcontextprotocol/go-sdk` v1.6.1 GA — Streamable HTTP via `mcp.NewStreamableHTTPHandler`. Auth DIY: `coreos/go-oidc` (Keycloak OIDC) + `oauthex.ProtectedResourceMetadata` for `/.well-known/oauth-protected-resource`. OpenAPI parse: `getkin/kin-openapi` (DIY mapper ~100 lines). Fallback `mark3labs/mcp-go` if needed.

## M1 — Backend OpenAPI (pure-additive, no behavior change) ✅
- [x] swaggo wired; `swag init -g cmd/server/main.go --parseInternal --parseDependency` → `internal/api/docs/swagger.json`.
- [x] 43 handlers annotated, clean operationIds + per-resource tags (admin/app/appserver/auth/database/endpoint/mlflow/model/observability/operation/project/quota).
- [x] Golden coverage test `openapi_coverage_test.go` — asserts every /api/v1 route present in spec. PASS.
- [x] `go:embed internal/api/docs/swagger.json` + public `GET /openapi.json`.
- [x] Verified: go build, go vet, go test green.
- NOTE follow-up: 7 endpoints bind anonymous request structs → typed `map[string]interface{}` (loose inputSchema). Extract named structs later for better tool schemas.

## M2 — Reflection engine + MCP skeleton ✅
- [x] Module `mcp-server/` (`github.com/dada-tuda/console/mcp-server`). Spec load file/HTTP, Swagger2→v3 via kin-openapi `openapi2conv.ToV3`.
- [x] toolgen: operation → MCP tool (name=operationId, inputSchema = path+query+flattened-body merge, ReadOnly/Destructive hints).
- [x] proxy handler: tool args → /api/v1, bearer from ctx; 202 → poll-getOperation hint; 4xx → IsError passthrough; 5xx/net → transient IsError.
- [x] `overrides.yaml` loader (rename/hide/annotate/group); hides logs/metrics noise.
- [x] Streamable HTTP transport (go-sdk v1.6.1 `NewStreamableHTTPHandler`). Bearer passthrough via SDK `req.GetExtra().Header` (request ctx NOT propagated — found + handled).
- [x] Tests: fixture-spec golden toolgen, proxy httptest (method/path/body/bearer/202/4xx/net), overrides, e2e vs REAL backend spec (39 tools). All pass. go build/vet green.

## M3 — Keycloak auth (high-risk, coupled, CROSS-REPO; no live verify from here)
Design: `tasks/keycloak-group-topology.md`. Decided: nested `/projects/<slug>/<role>` groups; full-path `groups` claim; provisioning = Crossplane Group CRs via gitops-agent (decision C); phased dual-write migration off `project_members`; keep `users` row (FK anchor) auto-provisioned by `keycloak_sub`.

### M3a — Backend OIDC identity + Principal ✅ (flag-gated, default local = reversible)
- [x] Config `AUTH_MODE=local|keycloak` (default local); KEYCLOAK_ISSUER/VERIFY_AUD/AUDIENCE/ROLES_CLIENT. JWT_SECRET required only in local mode.
- [x] `internal/auth/oidc.go` KeycloakVerifier (JWKS RS256, iss+exp, aud optional), KeycloakClaims (sub/email/groups/realm+resource roles).
- [x] migration 011: users.keycloak_sub (unique nullable). `ResolveUser` upserts/links users row by sub → Claims.UserID=users.id; all 50+ handlers untouched. Claims gains Groups/Roles.
- [x] KeycloakMiddleware + router selector by AuthMode; /auth/login → 410 in keycloak mode. Mock-JWKS hermetic tests (14) pass. go build/vet/test green.
- NOTE: ResolveUser live-DB path not exercised (no DB test harness) — verify on integration. SetupRouter panics on bad issuer in keycloak mode (fail-loud, intentional).

### M3b — Backend authz dual-read (phased)
- [ ] `getUserProjectRole` reads token groups `/projects/<slug>/<role>` when present, else project_members (dual-read). platform-admin from `/platform-admins`.

### M3c — gitops-agent KC Group CR rendering
- [ ] On project create/delete + member change → render Keycloak Group/Membership CRs into state repo (provider-keycloak). Backfill script from project_members.

### M3d — argo-infra manifests (author here, ArgoCD applies)
- [ ] Clients `dada-console` (PKCE, console redirects) + `dada-mcp`; Group Membership mapper full.path=true; parent groups `platform-admins`,`projects`.

### M3e — MCP Keycloak validation
- [ ] MCP validates Keycloak bearer (shared validator) + serves `/.well-known/oauth-protected-resource`.

### M3f — Frontend @dada/react-sso (oidc mode)
- [ ] SsoProvider oidc mode, `/callback` page, silent-renew, swap api.ts to getAccessToken(). Remove local login.

> Live verification (running Keycloak + cluster + ArgoCD) NOT possible from this environment. Code + manifests are unit-testable/lint-only here; end-to-end needs the cluster.

## M4 — Deploy
- [ ] Helm: MCP Deployment+Service+Ingress (`mcp.dada-tuda.ru`); spec via configmap/backend image.

## Review (per milestone)
- _pending_

---

# 2026-06-02 Compose Apps + Manual VM Connect + Live State

Design: `docs/architecture/compose-and-manual-vm-design.md`
Build order: ① Manual VM → ② Compose → ③ Live State. Each phase independently shippable.

## Phase 0 — Schema & shared groundwork
- [x] Migration `009_appserver_source.sql`: `app_servers.source` TEXT NOT NULL DEFAULT 'terraform' (`terraform`|`manual`), idempotent/privilege-tolerant
- [~] `apps` `runtime`/`app_server_id` — deferred to Phase 2 (compose), unused until then
- [x] `models/appserver.go`: add `Source` field (+ `AppServerSource` consts); updated List/Get scans
- [x] Migration runner auto-discovers `migrations/*.sql` (`backend/cmd/server/main.go` → `db.RunMigrations`)

## Phase 1 — Manual VM Connect
- [x] backend `CreateAppServer`: accept `mode` (`terraform`|`manual`); validate `{vm_ip, ssh_private_key}` for manual; SSH key kept out of audit metadata
- [x] backend: enqueue operation with mode + fields in payload (`models.CreateAppServerPayload` extended)
- [x] portainer-agent `worker`: `doCreateAppServer` branches on `mode=="manual"` → new `doCreateManualAppServer`
  - [x] `CreateEdgeEndpoint` → `CreateManualAppServer(source=manual)` → `SetAppServerProvisioned(ip,"manual")`
  - [x] `ssh.RunBootstrap(host, ssh_user, payload.ssh_private_key, bootstrapParams)` (reused; `dialAddr` supports custom port)
  - [x] `waitForAgent` → `SetAppServerReady` → `MarkReady`
- [x] portainer-agent: scrub `ssh_private_key` from `operations.payload` on terminal state via `db.ScrubOperationSecret` (deferred, both success+fail)
- [x] frontend: source toggle (Provision / Connect existing VM) + manual fields (IP, port, user, key textarea + "never stored" warning) + `manual` tag in list
- [x] Unit test: `dialAddr` (bare IP / explicit port / host)
- [ ] VERIFY (live): connect throwaway VM end-to-end → Ready; confirm `ssh_private_key` scrubbed in DB row

### Phase 0+1 Review
Implemented Manual VM connect reusing the existing edge-agent bootstrap path.
- **backend:** `appservers.go` validates `mode`; `operation.go` payload carries one-shot manual fields; audit metadata strips the key.
- **portainer-agent:** `doCreateManualAppServer` mirrors the Terraform flow minus provisioning; `CreateManualAppServer`/`ScrubOperationSecret` DB helpers; `dialAddr` enables custom SSH port without breaking the bare-IP caller.
- **frontend:** mode-aware create modal; `source` surfaced in types/API/list.
- **Verification:** `go build`+`go test` green in backend & portainer-agent; new `dialAddr` test passes; frontend `tsc` clean + `next build` success (15 routes). Only outstanding item is a live end-to-end VM connect (needs a real VM + Portainer).
- **Note:** one pre-existing eslint error in `apps/[appName]/values/page.tsx` (untouched by this work).

## Phase 2 — Compose App + two-pane editor  ✅
Decision refinement: compose = an App in a **VM-runtime environment** (env.runtime='vm' + env.app_server_id), not a new app column — the codebase already models this. Operation ownership split **by action** (gitops renders all; portainer owns CreateAppServer/DeleteAppServer/DeployStack), making the two claim sets disjoint and runtime-independent.
- [x] gitops `renderer`: `AppComposeGitPath`, `AppEnvGitPath`, `RenderComposeSkeleton`, `RenderEnvSkeleton`
- [x] gitops `DBWatcher.doCreateApp`: branches to `doCreateComposeApp` (AppServerName set) → commit compose.yaml + .env, snapshot runtime=compose, `EnqueueDeployStack`
- [x] portainer-agent `doDeployStack`: resolve endpoint via env→app_server (Ready+endpoint), `RedeployStack` if exists else `CreateStackFromGit`; dispatch case added
- [x] claim split: gitops `action NOT IN (CreateAppServer,DeleteAppServer,DeployStack)`; portainer `action IN (those three)`
- [x] backend `CreateApp`: VM runtime allowed; resolves env's AppServer, requires `Ready` (R2); image/port/profile validation helm-only; payload carries AppServerName
- [x] wstoken: `File` claim added (backend + gitops copies); **token authoritative** (handler ignores query params — fixes latent values-editor auth bug)
- [x] gitops `handleValuesWS` → `handleFileWS`: file resolved from claim, YAML check only for `*.yaml`, compose.yaml/.env save → `EnqueueDeployStackBySlug` (redeploy); `/ws/file` route + `/ws/values` alias
- [x] migration `010_system_user.sql`: fixed-UUID non-loginable actor for agent-initiated DeployStack ops
- [x] backend `GetValuesToken`: `file` query param (allow-list values.yaml|compose.yaml|.env), signed into claim
- [x] frontend: two-pane compose editor (`apps/[appName]/compose`), one WS per file; `valuesApi.getToken(file)`; app page routes compose apps to compose editor + hides helm-only Deploy Image
- [x] tests: wstoken File round-trip/tamper/expiry (backend); renderer compose/env paths; `resolveEditFile` (gitops); `composeGitPath` cross-agent contract (portainer)
- [ ] VERIFY R4 (live): Portainer reaches git repo with creds; create compose app → stack deploys; edit→save→redeploy. Needs live Portainer+VM+repo.

### Phase 2 Review
Compose apps now flow end-to-end as a GitOps two-phase pipeline: **gitops-agent renders** compose.yaml/.env into the app's git tree and enqueues a **DeployStack** op; **portainer-agent deploys** it onto the environment's AppServer endpoint via the existing Portainer stack API (create or git-redeploy). The editor was generalized to any file via a signed `File` claim (token is now authoritative, fixing a latent auth gap), with compose/.env saves auto-triggering redeploy. Frontend ships a two-pane compose editor.
- **Verification:** all three Go modules `go build` + `go test` PASS; frontend `tsc` clean, `eslint` clean on changed files, `next build` success (compose route present).
- **Scope notes / follow-ups:** (a) creating a compose app from the UI in a VM environment is supported by the backend but the apps create-modal UX for VM envs is minimal — image-optional form polish is a follow-up; (b) external-git-change live "update" push still only fires for values.yaml (gitwatcher regex), compose/.env editors load+save fine; (c) live R4 verification needs real infra.

## Phase 3 — Live State (read-only)  ✅
- [x] backend `internal/portainer`: lean read-only client (GetEndpoint, ListStacks, ListContainers, GetContainerLogs w/ docker stream de-mux); `New` returns nil when unconfigured (feature auto-disables)
- [x] backend config: `PORTAINER_URL`, `PORTAINER_API_TOKEN`; client wired into Handler
- [x] backend endpoints: `GET app-servers/:name/state` (endpoint heartbeat + containers, DB-status fallback), `GET .../apps/:name/state` (stack + compose containers), `GET .../apps/:name/logs?container=&tail=` (de-muxed, capped, non-follow)
- [x] frontend: `ComposeStatePanel` on compose app page (online dot, container list, per-container logs viewer, 10s auto-refresh); live online dot on app-servers list (best-effort per Ready VM)
- [x] tests: docker stream de-mux (multiplexed + raw/TTY passthrough), `New` disabled-when-unconfigured
- [ ] VERIFY (live): state matches Portainer UI; logs render. Needs real Portainer+VM.

### Phase 3 Review
Console now proxies live Portainer state read-only. A lean read-only client lives in `backend/internal/portainer` (separate from the agent's read-write client, since they're separate Go modules). Three endpoints expose VM heartbeat+containers, compose stack+containers, and container logs (Docker 8-byte stream headers stripped server-side). The feature self-disables when `PORTAINER_URL`/`PORTAINER_API_TOKEN` are unset (client is nil → 503 / DB-status fallback). Frontend shows a `ComposeStatePanel` (containers + logs, auto-refresh) on compose apps and a live online dot per Ready VM.
- **Verification:** backend `go vet` + `go test` PASS (incl. new de-mux + config tests); frontend `tsc` clean, `eslint` clean on changed files, `next build` success.
- **Follow-ups:** logs are tail-snapshot (not streaming/follow) in v1; VM detail page (containers for a specific server) not yet surfaced beyond the online dot — endpoint exists.

---

## Overall status (Phases 0–3)
All three features implemented + unit-verified; only **live end-to-end on real infra** remains across phases:
1. Manual VM connect — SSH-push edge agent, one-shot key scrubbed.
2. Compose apps — GitOps render → DeployStack → Portainer; two-pane compose.yaml/.env editor with redeploy-on-save.
3. Live state — read-only Portainer proxy (VM + app + logs).
New config to set in deploy: backend `PORTAINER_URL`, `PORTAINER_API_TOKEN`; portainer-agent already has `GITOPS_REPO_URL/BRANCH/USERNAME/TOKEN`. Migrations `009` (app_servers.source) + `010` (system user) run automatically on boot.

## Cross-cutting
- [ ] Tests per phase (Go unit + integration); separate authoring vs review pass before each merge
- [ ] Run real config after each fix (per global rules)

## Review (filled in as phases complete)
- _pending_

---

# 2026-05-29 values.yaml live editor (WS)

Design doc: `tasks/design-values-editor.md`

## gitops-agent
- [x] `internal/wstoken/token.go` — Sign/Verify HMAC токен (Claims: project, env, app, exp)
- [x] `internal/server/hub.go` — реестр WS-сессий, Notify по ключу project/env/app
- [x] `internal/server/ws_handler.go` — `/ws/values`: verify token → read file → send content → save loop → commit → InsertCommit
- [x] `internal/server/server.go` — добавить deps (pool, mgr, hub, tokenSecret), зарегистрировать `/ws/values`
- [x] `internal/worker/gitwatcher.go` — после обработки коммита: notify hub по изменённым values.yaml
- [x] `cmd/gitops-agent/main.go` — передать pool, mgr, hub в Server
- [ ] Тесты: wstoken Sign/Verify, hub Notify, ws_handler (httptest)

## console backend
- [x] `internal/wstoken/token.go` — дублировать пакет (~20 строк)
- [x] `internal/config/config.go` — GitopsAgentTokenSecret, GitopsAgentWSURL
- [x] `internal/api/apps_values.go` — POST .../values-token: canWrite + Sign + return {token, ws_url}
- [x] `internal/api/router.go` — зарегистрировать новый endpoint

## frontend
- [x] `npm install codemirror @codemirror/view @codemirror/state @codemirror/lang-yaml @codemirror/theme-one-dark`
- [x] `components/ui/yaml-editor.tsx` — CodeMirror controlled component
- [x] `lib/api.ts` — `valuesApi.getToken(projectId, envId, appName)`
- [x] `app/(console)/projects/[projectId]/apps/[appName]/values/page.tsx` — вкладка с WS-клиентом, редактором, Cmd+S, статус-индикатором
- [x] Добавить ссылку на вкладку Values в навигацию страницы приложения

## verification
- [x] `go test ./...` в gitops-agent — все зелёные
- [x] `go test ./...` в backend — все зелёные
- [x] `npm run build` во frontend — success, все 15 роутов
- [ ] E2E smoke: открыть вкладку → загрузился YAML → сохранить → тост committed

## Review
Три компонента реализованы и собираются без ошибок. Единственное что осталось — E2E smoke test в живом окружении и (опционально) unit-тесты wstoken/hub.

# 2026-05-29 GitOps app-local Helm layout

Intent: Make gitops-agent expect each app directory to own its Argo App descriptor plus Helm chart and values, instead of pointing App manifests back to top-level `helm/*`.

New canonical app tree:

```text
clusters/{cluster}/projects/{project}/environments/{env}/apps/{app}/
  app.yaml
  chart/
  values.yaml
```

- [x] Inspect gitops-agent render/watch paths and local argo-infra layout evidence
- [x] Update gitops-agent renderer helpers so App manifests point at app-local chart and values paths
- [x] Update gitops-agent write path so generated app changes commit all required app-local files
- [x] Update tests/docs/init snippets that encode the GitOps app structure
- [x] Run real verification gates for the touched areas

## Review

`renderer.go`: добавлены `AppHelmChartGitPath`/`AppHelmValuesGitPath` (возвращают `…/apps/{app}/chart` и `…/apps/{app}/values.yaml`); шаблон `appTmpl` теперь использует эти хелперы через FuncMap. `dbwatcher.go`: `doCreateApp` и `doDeployImageVersion` коммитят `app.yaml` + `values.yaml` атомарно через `commitFilesAndRecord`. `renderer_test.go`: `TestRenderApp` проверяет app-local пути, `TestGitPaths` покрывает оба новых хелпера. Все тесты (`go test ./...`) зелёные.

# Gitops Agent Project Sync

- [x] Inspect the repo-local gitops-agent and current state-repo bootstrap behavior
- [x] Add project bootstrap/write support so DB projects are mirrored to `project.yaml` in Git
- [x] Add git→DB handling for `project.yaml` so manual git changes win and sync back into the `projects` table
- [x] Update the state-repo init script and tests so first-start sync covers existing projects
- [x] Verify the gitops-agent package and relevant tests locally
- [x] Push the branch after verification

## Review

Added a project-level GitOps bootstrap/sync path to `gitops-agent`: DB projects now bootstrap to `clusters/beget-prod/projects/<slug>/project.yaml`, git-side `project.yaml` files are parsed back into the `projects` table, and the init script now seeds `client-a/project.yaml` too. Verified with `go test ./...` inside `gitops-agent` and pushed to `main`.

# Build on GitHub

- [x] Reproduce the current GitHub build surface and identify the missing piece
- [x] Add a GitHub Actions workflow that matches the release build path
- [x] Verify backend, frontend, Helm render, and Docker image build steps locally as far as the environment allows
- [x] Confirm the workflow file is present and ready for GitHub to pick up

## Review

Added a GitHub Actions workflow that mirrors the release build path from Jenkins and uploads the releaseable backend/frontend artifacts.

## 2026-05-14 console API base URL fix

- [x] Find why production frontend still targets `localhost:8080`
- [x] Move the local-dev API URL out of the production build path
- [x] Align Helm and CI on `NEXT_PUBLIC_API_URL=/api`
- [x] Render-check the Helm chart and confirm the config now matches the runtime intent

## Review

Production frontend had a build-time env leak: `frontend/.env.local` set `NEXT_PUBLIC_API_URL=http://localhost:8080`, and Next.js inlined that into the client bundle. I moved the local-only value to `frontend/.env.development.local`, set the CI frontend build to `NEXT_PUBLIC_API_URL=/api`, and renamed the Helm value key to `NEXT_PUBLIC_API_URL` so the chart matches the code.

# 2026-05-29 AI Studio hardening + VM track UI slice + prod push

- [x] Inspect current uncommitted AI Studio/probe/migration changes and preserve the valid parts
- [x] Verify current backend/frontend/agent gaps with real local commands and note missing run configurations
- [x] Finish AI Studio hardening slice:
  add tests for quota decision matrix, keep approval semantics correct, wire readiness/liveness/probe behavior, and keep migration 005 role-agnostic
- [x] Finish one VM-track feature slice end-to-end:
  expose environment runtime/app_server fields through the backend and frontend, add App Servers UI + API client/types, and make the project/app flows VM-aware where needed
- [x] Run repo verification gates for touched areas
- [ ] Prepare production push path with exact evidence and document what was or was not actually deployed

## Review

AI Studio hardening now has a pinned quota decision matrix, role-agnostic migration 005 default privileges, and split backend liveness/readiness probes with Helm pointing readiness at `/ready`. VM track now has environment runtime/app_server fields in the project DTO, frontend types/API helpers for AppServers, a project App Servers page with create/delete operation handoff, runtime badges on project/app screens, and an explicit VM-app deployment guard until the Portainer stack worker path is implemented. Verified locally with backend tests/build, gitops-agent tests/build, portainer-agent tests, frontend lint/build, and Helm lint/template.
