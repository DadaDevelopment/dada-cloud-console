# ADR-014: Move an app across projects (MoveApp)

Status: Proposed (Phase 1 building)
Date: 2026-07-14

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

### Phase 1 — stateless move (THIS change)

Scope: apps with NO persistent volume and NO attached ServiceDatabaseV2 that would
need data to travel. Covers the common case and prevents ad-hoc re-homes.

Preflight (backend, synchronous — a MoveImpact endpoint, modeled on delete-impact):
- resolve src (project,env,app) and dst project (+ its target env, default the
  prod env); require WRITE on both projects; reject name collision in dst.
- classify the app: list its volume (summary.volume) and attached children
  (the doDeleteApp cascade query). Return `blockers` (persistent volume, attached
  DB) and `movable` (domains, env vars, stateless children). If any blocker →
  Phase 1 refuses execution and the UI explains "needs data migration (Phase 2)".

Execution (gitops-agent `MoveApp` op, one orchestrated run):
1. Read src App snapshot desired spec (image/framework/port/replicas) + env_vars +
   child manifests (PublicApi/domains) from src.
2. Render the app under dst git path (dst namespace): app.yaml + values.yaml +
   secret + re-rendered PublicApi/domain manifests (same hostname, new ns).
3. Copy env_vars rows to dst (environment_id=dstEnv, same app_name), preserving
   is_secret/scope/encrypted value.
4. Commit dst → Argo brings the app up in the dst namespace.
5. Remove the src git folder (all app files) → Argo prunes the src namespace copy.
6. Repoint snapshot rows: UPDATE resource_snapshots SET project_id=dst, environment_id=dstEnv
   for the App row and every child matched by the cascade query. (Insert-or-update to
   respect the unique key; delete any src leftover.)
7. Standalone-owner caveat: if a child DB/S3 had empty app_ref it lives under
   `service-databases-<srcSlug>` — out of scope for Phase 1 (only app-attached
   resources move).

Downtime: brief — dst pods start while src is pruned; a domain briefly 404s while
its Ingress + cert re-issue in the new namespace. Acceptable for stateless.

Rollback: any failure BEFORE step 5 (src removal) → abort, leave src intact,
delete partial dst render. After step 5 the move is committed-forward (idempotent
re-run finishes it).

### Phase 2 — stateful move (persistent volume) — SPEC ONLY, gated on review

The RWO PV cannot mount in two namespaces at once, so this needs a stop-the-world
window and prod PV surgery. Because the whitelist is Retain-only, we rebind rather
than copy:
1. Scale src workload to 0 (unmount the volume). Verify no mounter.
2. Read the bound PV name from the src PVC. Patch the PV: clear `spec.claimRef`
   (Released → Available). PV data is retained (reclaimPolicy=Retain).
3. Render dst PVC with static `spec.volumeName=<PV>` in the dst namespace so it
   binds the SAME PV. Bring dst workload up. Verify Bound + data present.
4. Remove src (PVC + folder). PV now owned by dst.
Risk: a mistake here loses data. Requires: pre-move verification of Retain policy,
an explicit typed "I understand downtime + data" confirm, a dry-run that reports
the exact PV/claimRef patch without executing, and a feature flag defaulting OFF.
NOT to run on prod until this section is reviewed and the dry-run validated.

### Phase 3 — attached DB re-home — SPEC ONLY

The managed Postgres is Crossplane/external — its data does NOT live in a
namespace. Re-home = re-render the ServiceDatabaseV2 CR under the dst app's
resources.values.yaml with `spec.namespace=<dstNs>`, let Crossplane redeliver the
connection secret to dstNs, repoint the DB snapshot, and update the app's DB_URL
env var IF it is namespace-qualified. Lower data risk (no volume surgery) but
touches connection routing; specced separately, gated on review.

## Consequences

- Phase 1 ships a safe, useful move for the majority (stateless) and closes the
  ad-hoc re-home hole that caused orphans.
- Stateful moves are honestly deferred behind a reviewed, dry-run-gated path
  rather than shipped as risky one-shot PV surgery.
- MoveApp reuses delete-impact's scan + DeleteApp/CreateApp render paths; low net
  new surface for Phase 1.

## Alternatives rejected

- Metadata-only move (repoint rows, keep workload): impossible — workload lives in
  the src namespace; the app would run in the wrong isolation boundary.
- Delete + recreate by hand: loses env vars/domains/data and is the exact
  orphan-causing path we are replacing.
</content>
