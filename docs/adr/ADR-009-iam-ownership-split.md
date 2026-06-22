# ADR-009: IAM on Keycloak — Native RBAC as System of Record, Group-Encoded Org/Project Roles

## Status

Accepted — 2026-06-21

Supersedes the earlier draft of this ADR (user-service-as-authority + fat-claims + a custom Keycloak protocol-mapper SPI that fetched claims from user-service at token mint). That design was rejected: enriching the token via an HTTP call to user-service on every mint couples login availability to user-service, adds latency to the hot path, and reimplements — in custom code — RBAC that Keycloak provides natively. See **Rejected alternatives**.

Related: [PRD-IAM](../prd/PRD-IAM.md)

## Context

DADA Cloud needs orgs, project membership, project-scoped roles, scoped API keys, and service accounts. Identity already partly exists:

- **Keycloak** (shared `master` realm, `auth.dada-tuda.ru`): authentication, users, the hidden `/platform-admins` staff group. Already the signer of every token.
- **`user-service`** (Java/Spring): users, API keys (SHA-256, `sk-dada-` prefix), key→JWT exchange via `impersonatedLogin` (Caffeine-cached 5 min).
- **`dada-cloud`** (Go): `projects`, `project_members`, `environments` and its own role resolution (`getUserProjectRole`) + group-path RBAC for Keycloak mode.

Hard constraint from the product owner: **IAM is built on Keycloak, not a new implementation.** The corollary the owner pushed: **the token must be filled by Keycloak from data Keycloak owns — no extra request at mint, no custom enrichment shim.** Keycloak already has groups, roles, role-bearing group memberships, and client scopes. Org membership, project membership, and scoped roles map directly onto them. So we model the IAM domain *in Keycloak* and let stock mappers project it into the token.

## Decision

### 1. Keycloak is the system of record for identity + authorization

Orgs, membership, roles, and scopes live **in Keycloak** (groups + realm roles + client scopes), in the single shared realm. There is exactly one copy of authorization data — no sync, no pull, no dual-write.

- **`user-service`** shrinks to: API keys, service accounts, key→JWT exchange, and **Keycloak Admin-API orchestration** (create org = create groups + role-mappings; add member = join a role-bearing subgroup). Its `organizations` / `org_members` / `projects` / `project_members` tables are **not built** (the in-flight draft of them is dropped).
- **`dada-cloud`** is a **resource plane**: it owns project/environment **resource** rows (k8s, deploys, apps, monitoring) keyed by `project_id`, plus org-keyed billing/quota. It authorizes by **decoding native Keycloak claims** (group paths + roles + scopes). `getUserProjectRole` and local-mode role logic are deleted.
- **Keycloak** signs the token and fills the authz claims via stock mappers.

### 2. Native RBAC model — group-encoded scoped roles

Four **realm roles**, uniform at both scopes: `Owner`, `Admin`, `Developer`, `ReadOnly`.

Scope (which org / which project) + role is encoded in **role-bearing groups**. Keycloak group role-mappings are per-group, so "role within a scope" is a subgroup whose only job is to carry one realm role:

```
/orgs/{orgId}/{Role}                        → org-level role (e.g. /orgs/acme/Admin)
/orgs/{orgId}/projects/{projectId}/{Role}   → project-level role
```

Membership = authorization. Adding a user to `/orgs/acme/Admin` makes them org-Admin of `acme`; adding them to `/orgs/acme/projects/p1/Developer` makes them Developer on `p1`. A user can belong to many orgs and many projects simultaneously — multi-tenancy without realm sprawl.

**Org = group, NOT realm.** Realm-per-org is rejected (see alternatives): realms are heavyweight, do not scale to thousands of tenants, and shatter single-user-multi-org + cross-org SSO. Data isolation between orgs is enforced in dada-cloud by `org_id`, not by a Keycloak boundary.

### 3. Claims are emitted by stock Keycloak mappers — zero custom code in Keycloak

No custom provider JAR. No HTTP at token mint. Three built-in mappers on the client/scope:

- **Group Membership mapper** → `groups: ["/orgs/acme/Admin", "/orgs/acme/projects/p1/Developer", ...]` (full paths, all memberships).
- **Realm Role mapper** → `realm_access.roles`.
- **Client scopes** tied to realm roles → native `scope` claim. Role→scope mapping: `Owner/Admin` → all scopes incl. `admin`; `Developer` → `read, metrics:write, logs:write, deploy:write, builds:read, builds:write`; `ReadOnly` → `read, builds:read`.

### 4. dada-cloud decodes group paths

dada-cloud parses the `groups[]` claim with a deterministic decoder:

- `/orgs/{org}/{Role}` → org role for `{org}`.
- `/orgs/{org}/projects/{proj}/{Role}` → project role for `{proj}`.
- `/platform-admins` → internal staff god-mode (outside the enum).

Effective project role = `max(orgRole(projectOrg), projectRole)`; org Owner/Admin cascade into every project in that org. dada-cloud already owns the resource row keyed by `project_id`/`org_id`, so it knows each project's org locally — no lookup. This is ~tens of lines of pure string decode: dada-cloud stays dumb about role *resolution* (Keycloak assigned it), it only decodes Keycloak's own path encoding. Scopes read from the native `scope` claim.

### 4a. Producer of the group tree: gitops-agent (Option A, prod cutover decision 2026-06-22)

The model above says "user-service orchestrates the group tree via the Keycloak Admin API" (§5). At prod cutover this collided with reality: **gitops-agent already produces the Keycloak group tree declaratively** — it reads dada-cloud `project_members`, renders crossplane `Group`/`Roles`/`Memberships` CRs into the `keycloak-config` chart, and ArgoCD reconciles them. user-service's imperative Admin-API path (built but undeployed) would be a *second* producer mutating the same shared realm.

**Decision: gitops-agent stays the single producer** (Option A). It is the GitOps-native path — declarative CRs, reviewable in argo, reconciled, no imperative writes to the shared realm. user-service's group-orchestration is **not deployed**; its api-key + service-account work stands. `project_members` **stays in dada-cloud** as the membership source (contradicting §1's "dada-cloud owns no membership" — accepted: the working declarative machine wins over the doc).

Concretely:
- gitops-agent's `keycloak_group.go` renders the native topology: `/orgs/{slug}` (parent `orgs-container`) + four role subgroups `/orgs/{slug}/{Owner,Admin,Developer,ReadOnly}`, each with a group `Roles` CR mapping it to the same-named realm role (`iam-role-*`). Each dada-cloud project **is** an org; `projects.org_id` is backfilled to `projects.name` (migration 021) so the org-role cascade resolves.
- Legacy per-project groups (`projects-container/project-X/{legacy-role}`) and the legacy 4-role vocabulary are retired; `project_members.role` was rewritten to the uniform set in migration 019.
- Staff god-mode stays the hidden `/platform-admins` group (decoded directly); `AddPlatformAdminsToProject` is now a no-op.
- **Rejected — Option B (user-service owns groups via Admin API):** matches §5/§1 literally but replaces a working declarative producer with an imperative one on the shared `master` realm, and forces migrating `project_members` Go→Java. Higher risk, no upside over A.

### 5. user-service orchestrates Keycloak via Admin API

Membership/role/project mutations are Admin-API calls, done at **write time** (rare), not mint time (constant):

- Create org → create `/orgs/{orgId}` + the four role subgroups, map each to its realm role.
- Add member with role → join `/orgs/{orgId}/{Role}`. Change role → move subgroups.
- Create project → create `/orgs/{orgId}/projects/{projectId}/{Role}` subgroups; creator joins `Owner`. Then `POST /internal/projects` to dada-cloud to provision the resource row + default env.
- Service account = a Keycloak service-account principal in the right subgroup; `api_keys.principal_id` points at it (zero key-schema change). Jenkins = SA `jenkins-ci` in `/orgs/{org}/projects/{p}/Developer`.

### 6. API-key → JWT exchange unchanged in shape

Gateway still exchanges `sk-dada-…` → JWT via user-service `impersonatedLogin` (Caffeine-cached). The minted token carries the SA principal's group/role/scope claims through the same stock mappers — the key path and the human-login path produce identically-shaped tokens. Gateway stays a dumb router; no header injection, no fallback shim.

## Rejected alternatives

| Decision point | Options | Chosen | Why not the others |
|----------------|---------|--------|--------------------|
| Where authz data lives | user-service Postgres / **Keycloak native** / synced copy in both | **Keycloak native** | One copy; stock mappers; no sync machinery; no enrichment call. user-service Postgres-as-authority forces either a mint-time RPC or a write-time sync — both avoidable. |
| Token enrichment | **stock mappers** / custom protocol-mapper SPI pulling user-service / user-attribute write-before-exchange / gateway header injection | **stock mappers** | SPI-pull couples login to user-service uptime + adds hot-path latency + custom JAR doing network I/O inside Keycloak. Attribute-write races on concurrent exchange. Header injection moves trust off the signature. All are crutches for data that should just live in Keycloak. |
| Org boundary | **group** / realm-per-org | **group** | Realms are heavyweight, don't scale to many tenants, break one-user-many-orgs and cross-org SSO, and force dada-cloud to trust N issuers. Group = one realm, one user record, isolation via `org_id` downstream. |
| Scoped role encoding | **role-bearing subgroups** / per-user group role-mappings / user attributes | **role-bearing subgroups** | Keycloak role-mappings are per-group; subgroups are the native idiom for "role within a scope." Attributes would need a custom reshape mapper and lose native RBAC. |
| Claim shape consumed by dada-cloud | **decode native group paths in Go** / thin custom reshape mapper emitting `org_id/projects{}` | **decode in Go** | Keeps Keycloak 100% stock (no custom JAR at all); the decode is trivial and deterministic. A reshape mapper would re-introduce custom Keycloak code to avoid ~30 lines of Go. |
| Scope enforcement | **downstream per-service** / gateway central | **downstream** | Gateway shouldn't know every service's routes; scopes ride in the signed token. |

## Consequences

**Positive**
- Zero custom code in Keycloak; zero HTTP at token mint; login does not depend on user-service being up.
- One copy of authz data — no sync, no staleness window beyond token lifetime, no dual-write reconciliation.
- dada-cloud carries no authorization DB logic — pure claim decode. `getUserProjectRole` + local-mode role code deleted.
- Honors "build on Keycloak" maximally: orgs→groups, roles→realm roles, scopes→client scopes, multi-tenancy→group tree.
- Jenkins/agents get stable non-human identities; scopes finally enforced.

**Negative / risks**
- **Inverts the earlier authority decision.** user-service no longer owns membership; the in-flight `organizations/org_members/projects/project_members` entities + `/internal/principals/{id}/claims` endpoint (Chip 1) are dropped — roughly half that work is rebuilt as an Admin-API orchestration layer. dada-cloud claim parsing (Chip 3) changes from reading `org_role/projects` claims to decoding `groups[]` paths.
- Admin-API orchestration must be idempotent and handle partial failure (group created, role-mapping not) — reconcile/retry required.
- Group-tree growth: many orgs × projects × 4 roles = many (mostly empty) subgroups. Keycloak handles this, but create/cleanup must be lifecycle-managed (delete subgroups on project/org delete).
- Role/membership changes take effect on next token refresh (≤ token lifetime + 5 min gateway cache) — same bound as before, acceptable.
- `groups[]` claim grows with membership; a user in many projects ships a long path list. Mitigate later with a compact encoding or per-request org scoping if it bloats.

## Migration notes

1. **Keycloak realm config**: define the four realm roles; create client scopes mapping roles → scope sets; add Group Membership + Realm Role + scope mappers to the dada-cloud client (and the SA exchange client). Backfill one `/orgs/{orgId}` group per existing project owner; move existing members into role subgroups.
2. **user-service**: drop the org/member/project entity work; build the Admin-API orchestration (org/project/member CRUD → group + role-mapping calls); keep api-keys + service-accounts; keep key→JWT exchange (now just rides the stock mappers).
3. **dada-cloud**: delete `getUserProjectRole`/local-mode roles; add the `groups[]` path decoder + native `scope` reader; keep `POST /internal/projects` provisioner; update `permissions.go`, `models/user.go`, `rbac.ts` to the 4-role enum decoded from paths. Local dev mode mints a dev-god token (Owner of a dev org, all scopes) since there's no Keycloak locally.
4. **Roles**: remove the legacy set (`platform-admin, developer, client-admin, client-viewer`) everywhere; keep `/platform-admins` as out-of-enum staff god-mode.
