# Delete App + Project via UI, gated by cluster-truth impact preview

## Why
Users have no UI to delete an app or a whole project. The fear blocking a blind
delete: console cluster-discovery is DEFAULT-OFF, so `resource_snapshots` only
reflect git-authored + console-created resources. An app's `resources.values.yaml`
(which `doDeleteApp` nukes -> Argo prunes) can carry PublicApi (domains) + DB CRs
the UI never surfaced. Delete must first show the REAL blast radius from the
cluster, not from the snapshot.

## Architecture decision
Impact scan lives in the **backend** (it already builds in-cluster typed+dynamic
k8s clients in `backend/internal/cloudtask/*`). No gitops-agent round-trip. Delete
itself stays async: backend enqueues an operation, gitops-agent worker executes.

Impact = merge of TWO sources, deduped:
- **A. console-managed**: child `resource_snapshots` bound to the app (kind<>App,
  matched by summary_json app_ref/attached_app/spec.appRef/spec.attachedApp/
  spec.serviceName) — exactly what `doDeleteApp` cascades.
- **B. cluster-truth**: live list in the app's namespace via dynamic client:
  Ingress (networking.k8s.io/v1), PublicApi, ServiceDatabaseV2, S3Bucket,
  Certificate (cert-manager.io/v1), PersistentVolumeClaim — filtered to the app by
  label `dada.io/app=<name>` OR argocd instance `<name>-<env>` OR name==<name>/
  name prefix `<name>-`.
Each impact row is tagged `source: "console" | "cluster-only"`. cluster-only rows
(found in B but not A) are the danger set -> red banner in the modal.

## Backend (Go, gin)
New file `backend/internal/api/delete_impact.go`:
- `type ImpactItem struct { Kind, Name, Group string; Source string }`
  Group in {domain, database, storage, ingress, certificate, other}.
- `type DeleteImpact struct { App string; Namespace string; Items []ImpactItem; ClusterOnly int }`
- k8s client helper: copy the `rest.InClusterConfig()` + `dynamic.NewForConfig` /
  `kubernetes.NewForConfig` pattern from cloudtask; degrade gracefully (if no
  in-cluster config, return console-only impact with a `cluster_scan: false` flag,
  never 500).
- GVRs: publicapis, servicedatabasesv2, s3buckets (crossplane group used by
  existing renderers — reuse `pgvr(...)` group from gitops-agent renderer/statusreconciler
  as reference), certificates (cert-manager.io/v1), ingresses (networking.k8s.io/v1),
  persistentvolumeclaims (core/v1).

Handlers (register in `backend/internal/api/router.go`):
- `GET  /projects/:projectId/environments/:envId/apps/:appName/delete-impact` -> DeleteAppImpact
- `DELETE /projects/:projectId/environments/:envId/apps/:appName` -> DeleteApp
    enqueue op action='DeleteApp' resource_kind='App' resource_name=appName,
    payload `{"name": appName}`, environment_id=envId. Worker `doDeleteApp`
    ALREADY EXISTS (gitops-agent dbwatcher.go:872) — do NOT reimplement.
- `GET  /projects/:projectId/delete-impact` -> DeleteProjectImpact
    aggregate app impact over every env+app in the project + project-level rows
    (namespace(s), quota). 
- `DELETE /projects/:projectId` -> DeleteProject
    enqueue op action='DeleteProject' resource_kind='Project' resource_name=slug,
    payload `{}`.

RBAC/authz: reuse `effectiveRole` + `canWrite` gate exactly like DeleteAppServer.
All delete handlers return 202 {operation, message}. Impact handlers return 200.

Swagger: add full `// @...` annotations to every new handler, then regen
`swag init -o internal/api/docs` from backend/ (OpenAPI coverage gate fails CI
otherwise — see project_openapi_coverage_gate).

## gitops-agent worker: doDeleteProject (NEW)
`gitops-agent/internal/worker/` — register `case "DeleteProject": return w.doDeleteProject(ctx, op)`
in dispatch. Steps (FK-safe order, all scoped to op.ProjectID):
1. Remove git tree `clusters/beget-prod/projects/<slug>/` via RemoveAndPush (one
   commit) so Argo prunes every app/resource in the project.
2. DB, in a tx, FK order:
   `UPDATE git_commits SET operation_id=NULL WHERE operation_id IN (SELECT id FROM operations WHERE project_id=$1)`
   -> DELETE audit_events WHERE project_id=$1
   -> DELETE resource_snapshots WHERE project_id=$1
   -> DELETE operations WHERE project_id=$1 (EXCEPT the running op — mark it committed AFTER, or delete project row last and let cascade)
   -> DELETE FROM projects WHERE id=$1 (CASCADE handles members/environments/quotas/env_vars).
   NOTE the running DeleteProject op is itself in operations; delete-project row
   last and rely on ON DELETE CASCADE from projects->operations, OR mark the op
   committed BEFORE deleting operations. Pick the ordering that lets MarkCommitted
   still succeed (record commit sha first, then wipe). Simplest: capture sha,
   MarkCommitted, THEN run the DB wipe tx (which removes the op too) — MarkCommitted
   already persisted.
3. Delete namespace(s) `<slug>-<env>` — SKIP for MVP if worker has no k8s client;
   Argo/git prune + finalizers usually reap the ns. Leave a TODO + log. (Do NOT
   add k8s client to worker just for this in MVP.)
4. Keycloak: single-org collapse (project_single_org_dada) means there is NO
   per-project keycloak group to delete. SKIP. Do not touch keycloak.

Idempotent: missing git files skipped silently by RemoveAndPush; DB deletes are
WHERE-scoped no-ops if already gone.

## Frontend (Next.js, this repo's forked Next — read node_modules/next/dist/docs)
- API client methods in the same style as existing ones (find where DeleteAppServer/
  DeleteServiceDatabase clients live): `getAppDeleteImpact`, `deleteApp`,
  `getProjectDeleteImpact`, `deleteProject`.
- `DeleteImpactModal` component: fetches impact on open, spinner, then groups items
  by Group with counts; a prominent red warning banner listing `cluster-only` items
  ("Эти ресурсы есть в кластере, но консоль их не отслеживала — они будут удалены");
  confirm requires typing the exact app/project name; disabled button until match.
  On confirm -> call delete -> toast + navigate away + optimistic remove.
- Buttons: app page (danger zone section) -> app delete; project switcher/settings
  -> project delete. Both open DeleteImpactModal.
- i18n: add ru/en fragments (cookie-based console i18n, useT()) — see project_console_i18n.

## Verify
- backend: `go build ./... && go vet ./... && go test ./...`; swagger regen clean;
  `TestOpenAPICoverage` passes.
- gitops-agent: `go build ./... && go test ./...`.
- frontend: typecheck/build.
- preview: open app page, click delete, confirm modal renders impact + cluster-only
  banner, type-to-confirm gating works. Screenshot.
- Do NOT actually delete a real prod app during verify — test the modal/impact path
  against a scratch app or mock; the enqueue can be exercised without letting the op
  run to Committed, or use a throwaway app.

## Out of scope / caveats
- Namespace + keycloak teardown deferred (see worker steps 3-4).
- Backend ClusterRole must permit list/get on the scanned resources; extend the
  console helm chart rbac if the scan 403s (log + degrade to console-only, never
  500).
```
