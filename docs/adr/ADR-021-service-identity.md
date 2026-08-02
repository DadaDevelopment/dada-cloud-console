# ADR-021: ServiceIdentity — a credential belongs to an app, not to a project

Status: Proposed. Revised 2026-08-02 after owner review: the identity's
principal is the app, not the project.
Date: 2026-08-02

## Context

An app that talks to a platform service needs a platform credential. Today it
gets one by hand: an operator mints a key in the console (or in user-service),
pastes the literal into the app's `values.yaml` in argo-infra, and commits it.
`reels-tracker` carries `OPENROUTER_API_KEY: sk-dada-…` that way today.

Two things are wrong, and only the second one is the interesting one.

**The credential has no owner.** A literal in `env_vars` is indistinguishable
from `WHISPER_MODEL`, so nothing can maintain it: not rotation, not revocation,
not a move. MoveApp (ADR-014) carries the git render, `env_vars`, domains, an
attached ServiceDatabaseV2 re-point and the snapshot rows — it cannot carry an
identity, because there is no identity object to carry.

**The credential is issued at the wrong grain.** Every AI object in the schema
is keyed by project:

- `ai_gateway_keys.project_id NOT NULL REFERENCES projects(id) ON DELETE CASCADE`
  (migration 058), and `AIIntrospectKey` resolves a key through
  `JOIN projects p ON p.id = k.project_id` (`backend/internal/api/ai_keys.go`).
- `ai_routing_settings.project_id` is the PRIMARY KEY (migration 080).
- `agent_token_usage` records `project_id`/`env_id` and no app
  (`backend/internal/api/ai_gateway_usage.go`).

But a project is a *set of apps*. Issuing to the set means the credential
names something the app does not control and can be moved out of. On
2026-08-02 `reels-tracker` moved projects; the key stayed bound to the source
project, the source project was deleted, `ON DELETE CASCADE` took the key with
it, and every inference call returned `401 no credential for project/provider
openrouter` against a healthy app on an unchanged image.

The grain also loses information that already matters:

- **Blast radius.** A leaked key is every app in the project, because the key
  never named an app.
- **Attribution.** Two apps in one project produce one undifferentiated row in
  `agent_token_usage`. "Which app spent this" is unanswerable today.
- **Least privilege.** Scopes are per key, so per project; one app needing
  `ai:embeddings` grants it to all of them.

## Decision

Introduce **ServiceIdentity**: a first-class resource attached to an app, which
owns that app's platform credential for its whole lifecycle. **The identity's
principal is the app instance (`app_name` + `environment_id`). The project is
never stored on the credential — it is resolved at call time from wherever the
app currently lives.**

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

- **Cluster-scoped, like ServiceDatabaseV2.** The credential is issued by the
  console backend, which is not namespace-resident. `spec.namespace` selects
  only which namespace receives the delivered secret, exactly as it does for a
  database.
- **The delivered secret is `<appRef>-identity-credentials`**, holding
  `DADA_AI_API_KEY` and `DADA_AI_BASE_URL`, consumed by `secretKeyRef`. No
  credential value in git — the property that makes rotation and revocation
  possible at all.
- **`ai_gateway_keys` binds to the identity, not to a project.** `project_id`
  stops being a stored FK on the key.

### Introspection resolves the project live

`AIIntrospectKey` today reads `k.project_id` off the key row. It instead
resolves the identity's app to its current project:

```sql
SELECT rs.project_id
  FROM resource_snapshots rs
 WHERE rs.kind = 'App' AND rs.name = $1 AND rs.environment_id = $2
```

which is the same key MoveApp repoints in step 7 of ADR-014, and the same
lookup shape `apps.go:322` already uses. The gateway's own credential
resolution is unchanged: `loadAIProviderCredential` still filters
`project_id = $1 OR project_id IS NULL`; it just receives a project that is
current instead of one frozen at mint time.

**This is what makes a move a non-event.** The app changes project, the next
inference call resolves the new project, and the same key keeps working — no
re-mint, no revoke, no secret redelivery, no pod restart, no ordering window.
The reels failure cannot recur, because there is no moment at which the key
names a project the app has left.

### Reconcile

Reconciliation reduces to: if the identity has no active key, mint one and
deliver the secret. There is no project-change branch, because the project is
not part of the credential. Deleting the app deletes its identity and revokes
its key; deleting a project no longer cascades keys, because keys no longer
reference projects.

### MoveApp

`ServiceIdentity` joins the `classifyMoveChildren` movable set and is
re-rendered with `spec.namespace=<dstNs>` and the dst labels — the same edit
MoveApp already performs for ServiceDatabaseV2, and needed only so the secret
lands in the destination namespace. It carries no data and no invariants: none
of the ServiceDatabaseV2 Phase 3 rules (`metadata.name`, `spec.database`,
`spec.backup.*` verbatim) apply, because a re-delivered secret holds the same
key it held before.

### Attribution and scope follow the grain

Once the principal is the app, `agent_token_usage` gains the identity (and so
the app name), and per-app cost, per-app quota and per-app scopes become
expressible. `ai_routing_settings` stays project-keyed for now — routing policy
genuinely is a project-level choice — but nothing prevents an app-level
override later.

### What an app-grained identity still does not fix

`ai_provider_credentials` is BYOK and project-scoped by design (migrations
036/079). An identity guarantees the app keeps a valid *key*; it cannot conjure
a provider credential the destination project never held. `MoveImpact` must
therefore report, per attached identity, which providers the source project has
a row for and the destination does not — a warning, not a blocker, surfaced at
preflight instead of as a 401 an hour later. That was the second half of the
reels failure and no identity object closes it.

## Consequences

- A move stops touching credentials at all. The 2026-08-02 failure mode is
  removed rather than automated around.
- Blast radius, attribution and least privilege all become expressible, because
  the credential finally names the thing that uses it.
- No platform credential lives in git. Today `reels-tracker`'s key is readable
  by anyone with argo-infra access and cannot be rotated without a commit.
- Cost: a new CR kind, a schema change on `ai_gateway_keys` (drop the project
  FK, add the identity FK), an introspection rewrite, a MoveApp child class, and
  a migration for apps holding pasted keys. The introspection rewrite is the
  load-bearing part: it is on the hot path of every inference call, so the
  app→project lookup must be cached with the same TTL discipline as the
  gateway's existing introspect cache.
- Keys outlive projects. A key for an app whose snapshot row is missing must
  resolve to *invalid*, not to a default project — an unresolvable app is a
  revoked key, and that test is written before the migration lands.

## Alternatives rejected

- **Keep the project grain, re-mint the key on move** (this ADR's first draft).
  Every move becomes a credential rotation with a mint→deliver→revoke ordering
  window; get the order wrong and the running pod loses its key mid-move. It
  also leaves blast radius and attribution exactly as broken as they are today.
  Making the move cheap is strictly worse than making it unnecessary.
- **Teach MoveApp to rewrite the key env var.** Identity ownership implemented
  once, in the move path only, with the credential still sitting in git.
- **Drop the project FK without adding an app principal.** Kills the isolation
  boundary: a key with no principal reads any project's BYOK credentials.
- **A platform-wide `project_id IS NULL` row per provider.** How the reels bot
  accidentally survived, and what broke when the row went away. Bills every
  project's traffic to one key. Migration 079 deliberately limits this to
  providers the platform itself owns (nvidia_nim).
- **Keycloak service-account client per app now.** The right long-term home for
  service-to-service auth, but it does not issue AI gateway keys, so it does not
  close the live failure. Later phase, same resource, second payload.
