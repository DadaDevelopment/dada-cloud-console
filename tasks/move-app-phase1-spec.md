# MoveApp Phase 1 (stateless) — build spec

Read docs/adr/ADR-014-move-app-across-projects.md first. This spec is Phase 1 ONLY
(stateless apps). Stateful (persistent volume / attached DB) is OUT — detect and
BLOCK it, do not migrate data.

## Contract (fixed — backend + frontend build to this)

- `GET /projects/:projectId/environments/:envId/apps/:appName/move-impact?target_project_id=<uuid>` -> 200
  MoveImpact {
    app: string, src_project: string, target_project: string, target_env_id: string,
    target_namespace: string,
    movable: [{ kind, name, group }],      // env vars counted as one {kind:"EnvVars",name:"<n> vars"}
    blockers: [{ kind, name, reason }],    // persistent volume, attached ServiceDatabaseV2
    name_collision: bool,                  // app name already exists in target env
    can_move: bool                         // len(blockers)==0 && !name_collision
  }
- `POST /projects/:projectId/environments/:envId/apps/:appName/move` body { target_project_id } -> 202 {operation, message}
  409 with {error, blockers} if not movable.

## Backend

New `backend/internal/api/move_app.go`:
- MoveAppImpact handler: resolve src (project,env,app), target project + its env
  (pick the project's single prod/default env — mirror how env is resolved
  elsewhere; there is one implicit env per project now). Compute:
  - movable: domains (PublicApi child snapshots), env_vars count, other stateless
    children.
  - blockers: persistent volume (App snapshot summary_json.volume present) ->
    reason "persistent storage cannot cross namespaces yet (ADR-014 Phase 2)";
    attached ServiceDatabaseV2 child -> reason "attached database (ADR-014 Phase 3)".
  - name_collision: an App snapshot with same name already under target env.
  - Reuse the child-classification WHERE clause from delete_impact.go/doDeleteApp.
- MoveApp handler: authz requireWriter on BOTH src and target project; re-check
  can_move server-side; if blocked return 409; else enqueue op action='MoveApp'
  resource_kind='App' resource_name=appName, payload
  { app_name, target_project_id, target_env_id }. src project_id/environment_id go
  in the operations row's project_id/environment_id columns (like other app ops).
- Register routes in router.go. Add MoveAppPayload to models/operation.go.
- Swagger: annotate both handlers, regen `swag init -g cmd/server/main.go
  --parseInternal --parseDependency --outputTypes json -o internal/api/docs`.

## gitops-agent worker: doMoveApp (register case "MoveApp")

Study doCreateApp + doDeleteApp in dbwatcher.go first; REUSE their helpers
(projectEnv, managerFor, resolveRuntimeEnv, ensureAppExists, upsertManifestsFile,
commitFilesAndRecord, renderer.App* paths). Steps:
1. Resolve src slug/env/ns (op.ProjectID, op.EnvironmentID) and dst slug/env/ns
   (payload.TargetProjectID, payload.TargetEnvID) via projectEnv.
2. GUARD (defense in depth, backend already checked): reload the src App snapshot;
   if summary_json.volume present OR any attached ServiceDatabaseV2 child exists,
   return an error (do not move). Phase 1 must never touch a stateful app.
3. Read src App snapshot desired (image/framework/port/replicas/profile) and
   render the app under the DST git path (dst namespace): app.yaml + values.yaml +
   secret (from env_vars) + re-render each PublicApi/domain manifest the src app
   owned (same hostname, ServiceName=app, dst namespace). Mirror doCreateApp's
   assembly exactly, only project/env/namespace differ.
4. Copy env_vars: INSERT rows for (target_env_id, app_name, key, value_encrypted,
   is_secret, scope) from the src (environment_id, app_name) set. Keep encrypted
   bytes as-is (same GITOPS_ENCRYPTION_KEY). ON CONFLICT do nothing.
5. Commit dst files (one commit) -> Argo deploys in dst ns.
6. Remove src git folder (all AppGitPath siblings, like doDeleteApp's path list)
   in a second commit -> Argo prunes src ns.
7. DB, in a tx: repoint snapshots — UPDATE resource_snapshots SET
   project_id=target, environment_id=target_env WHERE the App row and its children
   (cascade WHERE clause). Handle the unique key (project_id,environment_id,kind,
   name): if a same-named target row somehow exists, delete src row instead.
   Then DELETE src env_vars for (src_env, app) after the copy is confirmed.
8. MarkCommitted with the dst commit sha.
Ordering/rollback: if step 3-5 fails, do NOT run step 6/7 (return error, src
intact, MarkFailed). Steps 6-7 after a successful dst deploy are forward-only;
make each idempotent so a re-run finishes.

## Frontend

- "Move to another project" button in the app danger-zone (app detail page), next
  to Delete. Opens MoveAppModal.
- MoveAppModal: dropdown of projects (projectsApi.list, exclude current). On
  select, fetch move-impact. Show movable summary; if blockers or name_collision,
  RED banner listing them and disable the Move button with the reason. Confirm
  (no type-to-confirm needed for a move; it's reversible-ish) -> POST move ->
  navigate to the operation like delete does.
- i18n ru/en (console i18n, useT()). Reuse delete-impact modal patterns/components.

## Verify
- backend: go build/vet/test ./... green; swagger coverage gate passes.
- gitops-agent: go build/test ./... green.
- frontend: tsc --noEmit + npm run build green.
- Do NOT commit/push (I integrate + review the worker op + run preview).
- NO inline // comments in Go (hook-blocked); doc-comments above funcs only.
</content>
