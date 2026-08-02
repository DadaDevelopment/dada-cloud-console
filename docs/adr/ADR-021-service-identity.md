# ADR-021: ServiceIdentity — one platform principal per app, for every service that authenticates

Status: Proposed. Revised twice on 2026-08-02/03: first after owner review
("issue to the app, not to the project"), then after the location columns were
checked against the schema and the app+environment pair turned out to move too.
Date: 2026-08-02

## Context

An app that talks to a platform service needs a platform credential. Today it
gets one by hand: an operator mints a key in the console, pastes the literal
into the app's `values.yaml` in argo-infra, and commits it. `reels-tracker`
carries `OPENROUTER_API_KEY: sk-dada-…` that way today.

Three things are wrong.

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

But a project is a *set of apps*. Issuing to the set means the credential names
something the app does not control and can be moved out of. On 2026-08-02
`reels-tracker` moved projects; the key stayed bound to the source project, the
source project was deleted, `ON DELETE CASCADE` took the key with it, and every
inference call returned `401 no credential for project/provider openrouter`
against a healthy app on an unchanged image.

The grain also loses information that already matters. A leaked key is every
app in the project, because the key never named an app. Two apps in one project
produce one undifferentiated row in `agent_token_usage`, so "which app spent
this" is unanswerable. Scopes are per key and therefore per project: one app
needing `ai:embeddings` grants it to all of them.

**Every new platform service invents its own credential table.** The AI gateway
has `ai_gateway_keys` (058). The payment gateway has `pay_service_keys` (083) —
`service TEXT NOT NULL UNIQUE`, a free-text service name, minted by hand,
pasted into a bot's config, with no link to any app, project or environment.
Both tables have the same shape (`token_hash`, `token_prefix`, `created_by`,
`last_used_at`, `revoked_at`) and the same lifecycle bugs, written twice. The
third service will write them a third time. A service that authenticates should
not have to design authentication.

## Decision

Introduce **ServiceIdentity**: one platform principal per app, owning one
revocable token that every platform service accepts, with scopes deciding what
that token may do. AI gateway and payment gateway become the first two
audiences; anything later is a scope, not a new table.

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
  `DADA_SERVICE_TOKEN` plus the endpoints the granted scopes imply
  (`DADA_AI_BASE_URL`, `DADA_PAY_BASE_URL`, …), consumed by `secretKeyRef`. No
  credential value in git — the property that makes rotation and revocation
  possible at all.

### The principal is a row, not a coordinate

The obvious principal is "the app instance", written as `(app_name,
environment_id)`. It is wrong, and the schema says so:

- `environments` is project-scoped — `project_id UUID NOT NULL REFERENCES
  projects(id)`, `UNIQUE(project_id, name)` (001).
- MoveApp moves an app to `p.TargetEnvID`, a different environment row in the
  destination project (`gitops-agent/internal/worker/move_app.go`).

So `environment_id` changes on a move exactly as `project_id` does, and an
identity keyed by that pair would be lost by the very operation this ADR exists
to survive. Resolving the project live from `resource_snapshots` does not save
it either: that lookup is itself keyed by `(project_id, environment_id, kind,
name)`.

Therefore the identity is a **row with a surrogate id**:

```sql
service_identities (
  id             UUID PRIMARY KEY,   -- the principal; never changes
  app_name       TEXT NOT NULL,      -- current location, re-pointed on move
  project_id     UUID NOT NULL,      -- current location, re-pointed on move
  environment_id UUID NOT NULL,      -- current location, re-pointed on move
  scopes         TEXT NOT NULL,
  ...
)
service_identity_tokens (
  identity_id UUID NOT NULL REFERENCES service_identities(id) ON DELETE CASCADE,
  token_hash  TEXT NOT NULL UNIQUE,
  ...
)
```

The token names `identity_id`. `project_id` and `environment_id` are *where the
identity currently lives*, not what it is — the same distinction
`resource_snapshots` already makes, and the reason MoveApp can re-parent a
snapshot row without the resource ceasing to exist.

Two tables, not one, so rotation is a new token row plus a revoke, and the
identity — and everything attributed to it — is untouched by rotation.

### A move re-points, it does not re-mint

MoveApp gains one statement, in the same transaction as
`repointMovedAppSnapshots` (ADR-014 step 7):

```sql
UPDATE service_identities
   SET project_id = $1, environment_id = $2
 WHERE app_name = $3 AND project_id = $4 AND environment_id = $5
```

and re-renders the CR with `spec.namespace=<dstNs>` and the dst labels, so the
delivered secret lands in the destination namespace. That is the whole move
path. **The token does not change**: no mint, no revoke, no secret redelivery,
no pod restart, no ordering window in which a running pod holds a dead key. The
2026-08-02 failure mode is removed rather than automated around.

The single statement is the single point of failure, so it is transactional
with the snapshot re-point and covered by a rehearsal test that asserts the
*same* token still authenticates after the move.

### Introspection

One endpoint, `POST /internal/identity/introspect`, returns the identity id, its
current project and environment, and its scopes. Each platform service checks
the scope it needs:

- the AI gateway needs `ai:chat` / `ai:embeddings`, and uses the returned
  project to select the BYOK `ai_provider_credentials` row exactly as it does
  today (`project_id = $1 OR project_id IS NULL` is unchanged);
- the payment gateway needs `pay:charge`, and uses the identity id where it
  uses `pay_service_keys.id` today — which also gives `service_charges` a real
  owner instead of a free-text service name.

Introspection is on the hot path of every inference call, so the response is
cached with the same TTL discipline as the gateway's existing introspect cache;
because the whole answer now comes from one row keyed by `token_hash`, the cache
holds a row lookup rather than a join.

A token whose identity row is missing resolves to **invalid** — never to a
default project. An identity outlives a project; it does not outlive its app.

### Not every consumer is an app

The payment gateway's first caller is a Telegram bot on a bare VPS with no
namespace and no App snapshot. Such a service gets an identity row with a NULL
location and no CR: the token is revealed once at mint time and configured by
hand, as it is today. It gains revocation, scopes and attribution; it does not
gain automatic delivery, because there is nothing to deliver into. This is a
deliberate second class, not an oversight — the alternative is pretending a VPS
is an app.

### What an identity still does not fix

`ai_provider_credentials` is BYOK and project-scoped by design (migrations
036/079). An identity guarantees the app keeps a valid *token*; it cannot
conjure a provider credential the destination project never held. `MoveImpact`
must therefore report, per attached identity, which providers the source
project has a row for and the destination does not — a warning, not a blocker,
surfaced at preflight instead of as a 401 an hour later. That was the second
half of the reels failure and no identity object closes it.

## Consequences

- A move stops touching credentials. It re-points one row and re-renders one
  namespace.
- Blast radius, attribution and least privilege become expressible, because the
  credential finally names the thing that uses it.
- The next platform service that needs authentication defines a scope, not a
  table, a key format, a hash column and a revocation endpoint.
- No platform credential lives in git. Today `reels-tracker`'s key is readable
  by anyone with argo-infra access and cannot be rotated without a commit.
- One token per app across all services means a leak reaches every scope that
  app was granted. Bounded by scopes and by the app grain — strictly smaller
  than today's project-wide AI key — and rotation is one operation instead of
  one per service. Accepted deliberately: the alternative, one token per
  audience, reintroduces per-service credential machinery, which is the third
  problem above.
- Cost: a new CR kind, two tables, an introspection endpoint, a MoveApp child
  class, migrations for both existing key tables, and a cutover for apps holding
  pasted keys.

## Alternatives rejected

- **Principal `(app_name, environment_id)`, project resolved live from
  `resource_snapshots`** (this ADR's second draft). `environments` is
  project-scoped and MoveApp assigns a new `environment_id`, so the principal
  moves with the app and the credential is lost by the operation it was
  designed to survive. The live lookup is keyed by the same moving coordinate.
- **Keep the project grain, re-mint the key on move** (first draft). Every move
  becomes a credential rotation with a mint→deliver→revoke ordering window; get
  the order wrong and the running pod loses its key mid-move. Leaves blast
  radius and attribution exactly as broken as they are today. Making the move
  cheap is strictly worse than making it unnecessary.
- **One token per audience under one identity.** Safer on leak, but every new
  service still needs its own issue/rotate/revoke path, which is the machinery
  this ADR exists to stop duplicating. Scopes carry the same bound at one
  token's cost.
- **Teach MoveApp to rewrite the key env var.** Identity ownership implemented
  once, in the move path only, with the credential still sitting in git.
- **Drop the project FK without adding a principal.** Kills the isolation
  boundary: a token with no principal reads any project's BYOK credentials.
- **A platform-wide `project_id IS NULL` row per provider.** How the reels bot
  accidentally survived, and what broke when the row went away. Bills every
  project's traffic to one key. Migration 079 deliberately limits this to
  providers the platform itself owns (nvidia_nim).
- **Keycloak service-account client per app now.** The right long-term home for
  service-to-service auth, but it issues neither AI gateway keys nor payment
  gateway keys, so it does not close the live failure. Later phase, same
  resource, second payload.
