# MVP: Infrastructure Adoption — AppServer console + Import (PRD-013/ADR-013)

Goal: adopt existing Docker Compose VM into Dada. Discovery already ships. Add Import
(discovery → compose → git → deploy) + redesign AppServer console (Vercel-simple,
Langfuse-convenient). Full vertical (backend + frontend).

## API contract (both sides build to this)

`POST /api/v1/projects/{projectId}/app-servers/{serverName}/import`  (deploy:write)

Request:
```json
{
  "app_name": "imported-stack",
  "services": [
    { "container_name": "web_1", "service_name": "web", "image": "nginx:1.25",
      "ports": ["80:80"], "volumes": ["data:/var/lib"], "include": true }
  ],
  "env": { "DATABASE_URL": "postgres://..." },
  "ack_secrets_in_git": true
}
```
Response 202: `{ "operation": {...}, "message": "..." }` (same envelope as /discover).
New op action: `ImportComposeStack`. Rejects if `ack_secrets_in_git=false` and env non-empty (409),
if VM not enrolled/Ready (409), if no services included (400).

## Backend (subagent: executor)
- [ ] `ImportComposeStackPayload` + action const in `backend/internal/models/operation.go`
- [ ] `ImportComposeStack` handler + swagger in `backend/internal/api/appservers.go`
- [ ] route in `backend/internal/api/router.go`
- [ ] `RenderComposeFromDiscovery` in `gitops-agent/internal/renderer/renderer.go`
      (services: image/ports/volumes-external/env_file; external-volume block mandatory; Prune:false)
- [ ] worker: render compose+.env → commit git → create app/env binding → DeployStack
      (follow CreateApp compose chain; reuse doDeployStack)
- [ ] swagger regen `swag init -o internal/api/docs`; `go test ./...` green (OpenAPI coverage gate)

## Frontend (me) — DONE (tsc 0, eslint 0, Next compile 200)
- [x] types + api: `appServersApi.import`, `ImportRequest`, `ImportServiceInput` in lib
- [x] AppServer detail redesign: tabbed shell (Overview / Workloads / Metrics / Logs),
      clean header, status/heartbeat, KPI cards, adopt hint on Overview
- [x] Workloads panel = discovery reframed as selectable cards (include/exclude, rename service,
      image/ports/volumes as chips, external-volume safety badge, warnings as callouts)
- [x] Import wizard modal: select → rename → optional .env → secret consent gate →
      compose preview → submit → poll → route to app
- [x] i18n keys (ru/en) for all new strings
- [x] a11y: focus states (amber ring), no emoji icons (SVG), hover transitions, aria-labels

## Verify
- [ ] `go test ./...` green
- [ ] frontend `npm run lint` + `tsc`/build green
- [ ] preview: discovery review + import wizard render + interactions

## Backend — DONE (agent, verified by me)
- [x] `ImportServiceSpec` + `ImportComposeStackPayload` (operation.go)
- [x] `ImportComposeStack` handler + swagger (appservers.go), route (router.go)
- [x] `RenderComposeFromDiscovery` + 5-case test (renderer.go/renderer_test.go) — external-volume pinning data-safe
- [x] `doImportComposeStack` worker (dbwatcher.go): render → commit → snapshot → enqueue DeployStack
- [x] swagger regenerated; `go test ./...` green (OpenAPI coverage gate passes)

## Verify — ALL GREEN
- [x] backend `go build ./...` 0, `go test ./...` 0 (incl internal/api coverage gate)
- [x] gitops-agent build 0, renderer+worker tests 0; portainer-agent build 0
- [x] frontend `tsc --noEmit` 0, `eslint` 0, Next dev compile 200, zero server errors

## Review
Full vertical shipped: discovery→import→compose→git→deploy end-to-end + AppServer console
redesign (tabbed, Vercel-simple).

Bug I caught + fixed (owned it): import op terminates at `Committed` (MarkCommitted), NOT
`Ready` — the deploy is a *separate* child DeployStack op. My wizard polled for `Ready` →
would false-timeout. Fixed: treat `Committed` as import-success, route to app page (shows
deploy progress). Confirmed by reading dbwatcher `commitFilesAndRecord`→`MarkCommitted` and
`deploy_stack.go`→`MarkReady` on the child op.

Known seams (MVP-scoped, documented):
1. env_vars table NOT persisted on import (agent seam) — first deploy `.env` correct, but
   later UI env-edit won't see them until re-set. Wire `resolveRuntimeEnv` in v2.
2. `environments.app_server_id` binding was schema-only (never set in code); import resolves
   the VM env — verify the bind path holds on a live enrolled VM before GA.
3. Live click-through not done — needs full stack (backend+Keycloak+Portainer+real AppServer).
   Static gates are the achievable proof this session.

Not committed. Changes in working tree.
