# ADR-014: Move an app across projects (MoveApp)

Status: Accepted. Phase 1 (stateless) SHIPPED. Phase 3 (attached DB) SHIPPED as an
Orphan-safe re-point with a cluster-scoped ownership handoff. Phase 2 (persistent
volume) is still spec-only: in-agent execution is NOT implemented and the move
deliberately aborts (see the flag note in Phase 2).
Date: 2026-07-14 (revised 2026-07-25 after the live telemost-bot and n8n prod moves)

## Context

Users want a "move app to another project" button. Today there is NO move logic
(grep: zero prior art). The only way an app changes projects is a hand-edit of
git paths + DB rows out-of-band — which is exactly what stranded the
example-project/fin-core `profi` snapshots (see the orphan-GC work, commit
149ac9f). A first-class MoveApp op makes re-homing safe and auditable.

### Why this is a cross-namespace migration, not a metadata edit

In this platform, project == namespace == isolation boundary:
- app git tree: `clusters/beget-prod/projects/<slug>/environments/<env>/apps/<app>/`
  (renderer.AppBaseGitPath).
- environment namespace: `<slug>-<env>` (backend projects.go:256, environments.namespace).
- snapshot rows keyed `(project_id, environment_id, kind, name)`.
- env_vars keyed `(environment_id, app_name)` (migration 013).
- child resources (ServiceDatabaseV2 / PublicApi / S3Bucket) are manifest entries
  INSIDE the app's `resources.values.yaml`, their CR `spec.namespace` = env
  namespace, and their snapshots link back via summary_json
  `app_ref` / `spec.appRef` / `attached_app` / `spec.serviceName`.
- persistent storage: summary_json.volume {path,size,storage_class}, rendered into
  values.yaml → helm/app-resources optional-pvc.yaml; storage-class whitelist is
  Retain-only.

So moving projects moves the namespace. Anything namespace-scoped (Deployment,
Service, Ingress, Secret, PVC) must be recreated in the target namespace and the
source pruned. There is no cheap metadata-only move.

## Decision

Deliver MoveApp in phases. A move is a saga (multi-step, non-atomic, resumable,
rollback-on-failure-before-cutover), not one commit.

### Phase 1 — stateless move (SHIPPED)

Scope: apps with NO persistent volume. An attached ServiceDatabaseV2 is no longer a
blocker — it moves with the app via the Phase 3 re-point below. Covers the common
case and prevents ad-hoc re-homes.

Preflight (backend, synchronous — a MoveImpact endpoint, modeled on delete-impact):
- resolve src (project,env,app) and dst project (+ its target env, default the
  prod env); require WRITE on both projects; reject name collision in dst.
- classify the app (pure `classifyMoveChildren`): list its volume (summary.volume)
  and attached children (the doDeleteApp cascade query). Return `blockers` (a
  persistent volume ONLY) and `movable` (domains, env vars, an attached
  ServiceDatabaseV2, stateless children). A persistent volume is the sole blocker →
  the move refuses and the UI explains "needs data migration (Phase 2)".

Execution (gitops-agent `MoveApp` op, one orchestrated run):
1. Read src App snapshot desired spec (image/framework/port/replicas) + env_vars +
   child manifests (PublicApi/domains, ServiceDatabaseV2) from src.
2. Render the app under dst git path (dst namespace): app.yaml + values.yaml VERBATIM
   (every running-app field survives, not just the summary subset) + secret +
   PublicApi/domain manifests + any ServiceDatabaseV2 RE-POINTED to the dst namespace
   (Phase 3 — an Orphan-safe re-home, never a rename).
3. Copy env_vars rows to dst (environment_id=dstEnv, same app_name), preserving
   is_secret/scope/encrypted value.
4. Commit dst → Argo brings the app up in the dst namespace.
5. Pre-adopt the cluster-scoped resources (PublicApi AND ServiceDatabaseV2): re-stamp
   BOTH ArgoCD ownership markers to the target app BEFORE the source prune (see Phase 3
   / the cluster-scoped handoff note). Skipping this lets the source app prune the
   single shared object mid-move.
6. Remove the src git folder (all app files) → Argo prunes the src namespace copy.
7. Repoint snapshot rows: UPDATE resource_snapshots SET project_id=dst, environment_id=dstEnv
   for the App row and every child matched by the cascade query. (Insert-or-update to
   respect the unique key; delete any src leftover.)
8. Standalone-owner caveat: if a child DB/S3 had empty app_ref it lives under
   `service-databases-<srcSlug>` — out of scope (only app-attached resources move).

Downtime: brief — dst pods start while src is pruned; a domain briefly 404s while
its Ingress + cert re-issue in the new namespace. Acceptable for stateless.

Rollback: any failure BEFORE step 5 (src removal) → abort, leave src intact,
delete partial dst render. After step 5 the move is committed-forward (idempotent
re-run finishes it).

### Phase 2 — stateful move (persistent volume) — DELIBERATELY NOT EXECUTED IN-AGENT

A Longhorn RWO volume cannot mount in two namespaces at once, and the real live
moves (2026-07-23/24) proved the safe path is copy-into-a-fresh-RWX volume, not a
claimRef rebind: snapshot the source volume while still attached, back it up, then
restore into a Retain PV that the dst chart's PVC binds by static
`spec.volumeName`, with `spec.frontend: blockdev` on the restore and unmounted
source replicas skipped. That whole procedure is implemented and proven in the
standalone `tools/dbmove` operator tool (Part A, 40 tests, dry-run validated) —
run out-of-band by an operator, NOT inside the MoveApp saga.

In-agent MoveApp therefore does NOT copy volume data. The worker gates on a
`MOVE_VOLUME_ENABLED` env flag (default OFF):
- flag OFF: a volume-bearing app aborts the move before any git write ("moving
  stateful apps is disabled").
- flag ON: the worker STILL aborts, with a distinct message, because in-agent
  volume copy is not implemented — landing the app on a fresh empty PVC would
  silently lose its data. The flag is forward plumbing for the execution
  follow-up, never a switch that skips the copy.
The backend keeps a persistent volume a HARD blocker regardless of the flag, so
the console never advertises a move the worker will refuse. Both sides flip
together only once the copy-into-RWX step is automated in-agent (or MoveApp
learns to drive `tools/dbmove`). Until then, moving a volume-bearing app = run
`tools/dbmove` by hand.

### Phase 3 — attached DB re-home (SHIPPED, Orphan-safe re-point)

The managed Postgres data does NOT live in the app's namespace: a
ServiceDatabaseV2 is a logical database inside the SHARED `postgresql-0` cluster
(namespace `databases`), provisioned by a Crossplane `Database` managed resource
whose `deletionPolicy: Orphan` and which is namespace-independent. The
ServiceDatabaseV2 XR itself is CLUSTER-SCOPED — it has no `metadata.namespace`;
its `spec.namespace` only selects which namespace receives the delivered
`<appRef>-db-credentials` secret.

So a re-home moves NO data. The worker re-renders the same CR with
`spec.namespace=<dstNs>` and the dst `dada.io/project|environment|operation`
labels, and Crossplane redelivers the credentials secret into the dst namespace.

Data-safety invariants (violating any of these turns a re-point into a
Crossplane delete+create against the shared cluster = data loss). These are
carried VERBATIM from the source manifest, never recomputed:
- `metadata.name` (a rename provisions a fresh empty database),
- `spec.appRef`,
- `spec.database` (the logical DB name — changing it strands the real data),
- `spec.backup.*` (enabled/frequency/retention; the real default frequency is
  `daily`, NOT `@daily` — `@` is a reserved YAML indicator).
ONLY `spec.namespace`, the three dada.io labels, and the OperationID change.

The cluster-scoped handoff (the non-obvious part). Because the XR is
cluster-scoped and keyed by name, the source and target app renders point at the
very SAME single live object — exactly like a custom-domain PublicApi. The object
carries the source app's two ArgoCD ownership markers: the
`argocd.argoproj.io/tracking-id` ANNOTATION (authoritative for prune ownership)
and the `argocd.argoproj.io/instance` LABEL. If the source git folder is pruned
(Phase 1 step 6) while those still point at the source app, the source Argo app
prunes the DB composite as its own orphan — tearing down the credentials secret
(the logical database survives via Orphan, but the app loses its connection secret
and a recreate may rotate the Postgres password) for the window until the target
reconciles. So before the source prune, the worker re-stamps BOTH markers onto the
target app (`preAdoptClusterScopedResources`), with the tracking-id's namespace
segment = dstNamespace (the exact value the target app computes), a zero-downtime
handoff. Best-effort: no in-cluster client or a not-yet-created object logs and
proceeds — the move still completes, worst case the pre-fix blip, never a failed
move.

Safety net: before enqueuing the move the backend fires a best-effort pre-move
logical backup (Kanister, kind `pre-move`) of each attached database, giving a
restore point taken immediately before the re-point. Skipped when Kanister is not
configured; a failure is logged, not fatal (the re-point never touches the logical
data).

## Consequences

- Stateless apps AND DB-attached apps move first-class and safely, closing the
  ad-hoc re-home hole that caused orphans. The DB re-point moves no data (shared
  logical DB, Orphan policy), so it carries far less risk than volume surgery.
- The cluster-scoped ownership handoff is the load-bearing subtlety: a DB (like a
  custom-domain PublicApi) is one shared object both renders point at, so ownership
  must be handed to the target BEFORE the source prune. This was learned live —
  patching only the instance label was insufficient; the tracking-id annotation is
  authoritative for prune.
- Volume moves are honestly deferred: the copy-into-RWX procedure exists and is
  proven in `tools/dbmove` (operator-run, out-of-band), but is NOT wired into the
  in-agent saga. The `MOVE_VOLUME_ENABLED` flag can never cause a silent
  empty-PVC landing — flag-ON still aborts. No data-loss path ships.
- MoveApp reuses delete-impact's scan + DeleteApp/CreateApp render paths; low net
  new surface.

## Alternatives rejected

- Metadata-only move (repoint rows, keep workload): impossible — workload lives in
  the src namespace; the app would run in the wrong isolation boundary.
- Delete + recreate by hand: loses env vars/domains/data and is the exact
  orphan-causing path we are replacing.
- Volume move by claimRef rebind (clear the source PVC's claimRef, bind the same
  Retain PV in the dst namespace): rejected in favour of copy-into-fresh-RWX. The
  live moves showed the app needed RWX (not the source's RWO) and that rebinding a
  still-referenced PV risks a two-mounter window; a snapshot+restore into a fresh
  volume is the recoverable path (`tools/dbmove`).
- DB re-home by rename / new database: rejected — in a shared Postgres cluster,
  changing `metadata.name` or `spec.database` makes Crossplane drop and recreate
  the logical database, destroying the data. Only `spec.namespace` may change.
</content>
