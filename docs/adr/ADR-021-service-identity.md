# ADR-021: ServiceIdentity — an app's platform credential is provisioned, not pasted

Status: Proposed.
Date: 2026-08-02

## Context

An app that talks to a platform service needs a platform credential. Today it
gets one by hand: an operator mints a key in the console (or in user-service),
pastes the literal into the app's `values.yaml` in argo-infra, and commits it.
`reels-tracker` carries `OPENROUTER_API_KEY: sk-dada-…` that way today.

A pasted literal is not owned by anything, so nothing can maintain it. The live
failure on 2026-08-02 is the whole argument:

- The app moved to another project. MoveApp (ADR-014) carries the git render,
  `env_vars`, domains, an attached ServiceDatabaseV2 re-point and the snapshot
  rows. It carries no identity, because there is no identity object to carry —
  the credential is an opaque string inside `env_vars`, indistinguishable from
  `WHISPER_MODEL`.
- The credential stayed bound to the source project. `ai_gateway_keys.project_id`
  is `NOT NULL REFERENCES projects(id) ON DELETE CASCADE` (migration 058), and
  `AIIntrospectKey` resolves a key by `JOIN projects p ON p.id = k.project_id`
  with `revoked_at IS NULL` (`backend/internal/api/ai_keys.go`). A key whose
  project is gone resolves to nothing.
- Provider credentials are project-scoped too: `loadAIProviderCredential` reads
  `WHERE provider = $2 AND (project_id = $1 OR project_id IS NULL)`
  (`backend/internal/api/ai_credentials.go`). So an app that changes project
  loses every BYOK credential its old project held, silently, at the next
  inference call.

The observed result: the key introspected to project
`6042008f-a66d-4b03-add5-f4c3dd107d62`, which no longer exists in `projects`,
and every call returned `401 no credential for project/provider openrouter`.
The app was healthy, the image was unchanged, the deploy was green. Nothing in
the system said "this app's credential no longer belongs to this app's project",
because nothing in the system knew the credential belonged to the app at all.

ServiceDatabaseV2 already solved this shape of problem for Postgres. An app
declares a database; the platform provisions it, delivers
`<appRef>-db-credentials` into the app's namespace, and MoveApp re-points
`spec.namespace` so the credentials follow the app across projects without the
operator ever seeing a password. Identity is the same problem with a different
payload.

## Decision

Introduce **ServiceIdentity**: a first-class, app-attached resource that owns
an app's platform credential for its whole lifecycle — create, move, rotate,
delete. Modeled on ServiceDatabaseV2 deliberately, so it inherits MoveApp's
re-point path instead of inventing a second one.

```yaml
apiVersion: platform.dada-tuda.ru/v1alpha1
kind: ServiceIdentity
metadata:
  name: reels-tracker-identity
  labels:
    dada.io/project: internal
    dada.io/environment: prod
    dada.io/operation: op-…
spec:
  appRef: reels-tracker
  namespace: internal-prod
  scopes: "ai:chat ai:embeddings"
```

Shape, and why each part is the way it is:

- **Cluster-scoped, like ServiceDatabaseV2.** The credential is issued by the
  console backend, which is not namespace-resident. `spec.namespace` selects
  only which namespace receives the delivered secret, exactly as it does for a
  database.
- **The delivered secret is `<appRef>-identity-credentials`**, holding
  `DADA_AI_API_KEY` and `DADA_AI_BASE_URL`. The app consumes it by
  `secretKeyRef`. No credential value ever enters git — which is the property
  that makes every later step (move, rotate, revoke) possible at all.
- **Project binding is derived, never written in the spec.** The owning project
  is whatever project the app is in, read at reconcile time from the app's
  snapshot row. A spec field would be a second source of truth that MoveApp
  would have to remember to update, which is the failure being fixed.
- **The key is minted through the existing path**, an `ai_gateway_keys` row
  with an `sk-dada-ai-` token, so introspection, scopes, usage accounting and
  the console key list all keep working unchanged. ServiceIdentity is an owner
  for that row, not a parallel issuer.

### Reconcile

The console backend owns reconciliation; it already holds both the DB pool and
an in-cluster client (it reads `<db>-db-credentials` today,
`backend/internal/cloudtask/dbcreds.go`). For a ServiceIdentity whose desired
project is P:

1. If no active `ai_gateway_keys` row exists for (identity, P), mint one.
2. Write/update the secret in `spec.namespace`.
3. Revoke any active row for the same identity bound to a project other than P.

Ordering is load-bearing: mint and deliver **before** revoking the old key, so
a running pod is never without a valid credential. Step 3 after step 2 makes a
move zero-downtime for the same reason the cluster-scoped ownership handoff in
ADR-014 happens before the source prune.

### MoveApp

`ServiceIdentity` joins the `classifyMoveChildren` movable set. The move
re-renders it with `spec.namespace=<dstNs>` and the dst labels — the identical
edit MoveApp already performs for ServiceDatabaseV2. Reconcile then observes
that the app's project changed, mints a key in the destination project,
redelivers the secret, and revokes the source key. The app restarts on the new
secret and keeps working.

Unlike a database, an identity carries **no data**, so it needs none of the
Phase 3 invariants (`metadata.name`, `spec.database`, `spec.backup.*` carried
verbatim). A rotated identity is a new key, not a lost one — the whole point is
that the credential value is disposable while the binding is not.

### Provider credentials

An identity restores an app's *key*. It cannot restore a provider credential
the destination project never had: `ai_provider_credentials` is BYOK and
project-scoped by design (migration 036/079). MoveImpact must therefore report,
per attached identity, which providers the source project holds credentials for
and the destination does not — a warning, not a blocker, surfaced before the
move rather than as a 401 an hour later.

This is the second half of the reels failure and no identity object fixes it by
itself. Naming it at preflight is the fix.

## Consequences

- An app's credential survives a project move, a project rename and a project
  delete-and-recreate, because the binding is a resource the platform
  reconciles rather than a string an operator remembered to update.
- No platform credential lives in git. Today `reels-tracker`'s key is
  world-readable to anyone with argo-infra access and cannot be rotated without
  a commit; after migration it is a k8s Secret with a rotation path.
- Rotation becomes an operation instead of an incident: mint, deliver, revoke,
  restart — the same three steps reconcile already runs.
- Cost: a new CR kind, a reconciler, a MoveApp child class, and a migration for
  apps currently holding pasted keys. All of it rides on paths ADR-014 already
  built; the new surface is the reconciler.
- `ON DELETE CASCADE` stops being a trap: deleting a project still drops its
  keys, but the identity is attached to the app, so a moved app is re-minted
  rather than orphaned.

## Alternatives rejected

- **Teach MoveApp to rewrite the key env var.** It would have to recognise a
  credential inside `env_vars`, mint a replacement and rewrite git. That is
  identity ownership implemented once, in the move path only, with the
  credential still in git — the leak and the rotation gap both stay.
- **Make keys project-independent (drop the FK).** Kills the isolation boundary
  the platform is built on: a key would keep reading a project's BYOK
  credentials after the app left it.
- **A platform-wide `project_id IS NULL` row per provider.** Already how the
  reels bot accidentally survived, and exactly what broke when the row went
  away. It bills every project's traffic to one key and makes an app's access
  depend on a row no project owns. Migration 079 deliberately limits this to
  providers the platform itself owns (nvidia_nim).
- **Keycloak service account per app now.** The right long-term home for
  service-to-service auth, but it does not issue AI gateway keys, so it does
  not fix the live failure. Phase 2, behind the same resource.
