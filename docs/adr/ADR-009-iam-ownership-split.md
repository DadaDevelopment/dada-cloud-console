# ADR-009: IAM Ownership Split — user-service as Single Authority, Fat JWT Claims

## Status

Proposed — 2026-06-21

Related: [PRD-IAM](../prd/PRD-IAM.md)

## Context

DADA Cloud needs orgs, project membership, project-scoped roles, scoped API keys, and service accounts. Identity is already partly built and **scattered across two services**:

- **`user-service`** (Java/Spring): owns users, API keys (SHA-256, `sk-dada-` prefix, `scopes` field present but unenforced), and Keycloak provisioning. Gateway exchanges API-key→JWT via `impersonatedLogin` (Caffeine-cached 5 min). No orgs, projects, members, roles, or service accounts. JWT carries only `sub` + `realm_access.roles`.
- **`dada-cloud`** (Go): already owns `projects`, `project_members`, `environments` tables and resolves project roles itself (`getUserProjectRole`), plus group-path RBAC for Keycloak mode. AI-model API keys only; no generic keys or service accounts.

The hard constraint from the product owner: **IAM must be based on Keycloak + user-service, not a new dada-cloud implementation.** The classic young-platform mistake is writing your own IAM before the first ten customers — Keycloak already solves 90%.

Tension: project membership and roles physically live in dada-cloud today, but the directive says identity belongs to user-service. And we want dada-cloud to be "dumb" about authorization.

## Decision

### 1. user-service is the single authority for identity and membership

`user-service` owns: users, **organizations**, **all membership (org + project)**, **roles**, **API keys**, **service accounts**, Keycloak provisioning. It **mints projects**.

`dada-cloud` is demoted to a **resource plane**: it owns project/environment **resource** rows (k8s, deploys, apps, monitoring) keyed by `project_id`, and authorizes purely from JWT claims. `getUserProjectRole` and local-mode role logic are deleted. `project_members` ownership moves to user-service.

### 2. Fat JWT claims

The token carries everything dada-cloud needs — no hot-path RPC, no DB role lookup:

```json
{
  "sub": "<principal_id>",
  "org_id": "<org>",
  "org_role": "Owner|Admin|Developer|ReadOnly",
  "projects": { "<project_id>": "<role>" },
  "scopes": ["read","metrics:write","logs:write","deploy:write","builds:read","builds:write","admin"]
}
```

Effective project role = `max(org_role, projects[project_id])`. user-service builds these claims at login / key exchange.

#### Claim injection mechanism (DECIDED 2026-06-21)

The token is minted and **signed by Keycloak** (token-exchange / impersonatedLogin); user-service cannot sign claims into it directly. Separate the three jobs:

- **Data authority** (who is in what org/project, role, scopes) = **user-service** Postgres. Unchanged.
- **Signing** = **Keycloak** (only it can).
- **Projection** (data → claim in the signed token) = **Keycloak**, via a **custom protocol-mapper SPI**.

**Decision:** a Keycloak protocol-mapper SPI (deployed provider JAR) calls user-service `GET /internal/principals/{principal_id}/claims` at token-mint time and injects `org_id, org_role, projects{}, scopes[]` into the signed JWT. No Keycloak user-attribute writes — reading live avoids the concurrent-exchange race and staleness. user-service stays the data authority and exposes the internal claims endpoint; Keycloak owns projection + signing; gateway stays a dumb router.

`GET /internal/principals/{principal_id}/claims` → `{ org_id, org_role, projects: {project_id: role}, scopes: [...] }` (the FatClaims object).

**Rejected — user-attributes + built-in mapper:** writing claims as Keycloak user attributes before exchange is mutable global per-user state → races on concurrent exchange, goes stale, needs out-of-repo realm config.

**Fallback (only if operating a Keycloak provider is refused):** user-service returns the FatClaims object in the `/v1/apikeys/exchange` response and gateway attaches it as a trusted internal header (stripping any client-supplied copy). Bends this ADR — claims no longer ride in the signed JWT; downstream trusts a gateway header instead of the signature.

**Constraint:** the SPI deploys into the shared **master** realm (auth.dada-tuda.ru) — affects all realm consumers; coordinate the rollout.

### 3. Uniform 4-role model

`Owner, Admin, Developer, ReadOnly` at both org and project scope. The legacy set (`platform-admin, developer, client-admin, client-viewer`) is removed everywhere. The hidden Keycloak group `/platform-admins` is kept as internal staff god-mode — never a customer role, never in UI.

### 4. First-class service accounts

`service_accounts` is a non-human principal (`id, org_id, name, role`). `api_keys.principal_id` already points at a principal, so SAs reuse the key table with zero schema change. Jenkins = SA `jenkins-ci`, role Developer, scoped key.

### 5. Scope enforcement downstream

Scopes ride in fat claims. Each service guards its own routes (`requireScope(...)`). Gateway stays a dumb router. Vocabulary: `read, metrics:write, logs:write, deploy:write, builds:read, builds:write, admin`.

### 6. user-service mints projects

`POST /orgs/{org}/projects` → user-service generates `project_id` + Owner membership → calls dada-cloud `POST /internal/projects` to provision the resource row + default env. Membership exists at mint time, so fat claims work immediately.

## Alternatives considered

| Decision point | Options | Chosen | Why not the others |
|----------------|---------|--------|--------------------|
| Org layer | real table / fake via Keycloak groups | **real table** | Billing/quotas/keys/SAs need a real parent; groups leave them homeless |
| Where membership lives | dada-cloud / split / user-service | **user-service (all)** | Fat claims need roles at exchange time; split forces a sync (push/pull) that loses instant role changes |
| Claims | fat / thin+dada-cloud resolves / thin+RPC | **fat** | Owner wanted dada-cloud dumb; no hot-path RPC, no DB role logic in Go |
| Project minting | user-service / dada-cloud | **user-service** | project_id is identity; must exist before membership/claims |
| Service accounts | first-class principal / labeled key | **first-class** | Stable identity across key rotation; clean audit; reuses `principal_id` |
| Scope enforcement | downstream / gateway central | **downstream** | Gateway shouldn't know every service's routes |

### Trade-off accepted

Fat claims + moving `project_members` to user-service means: (a) a JWT bloats with many projects, and (b) role changes take effect only after token refresh (≤ ~4h expiry + 5 min gateway cache). Accepted: simpler dada-cloud, no sync machinery, no RPC on the hot path. Mitigate bloat later with a compact encoding if needed.

## Consequences

**Positive**
- dada-cloud carries zero authorization DB logic — pure claim reads. `getUserProjectRole` and local-mode role code deleted.
- One authority for identity; honors the "don't build your own IAM" directive.
- Jenkins/agents get stable non-human identities.
- Scopes finally enforced.

**Negative / risks**
- Migration: `project_members` moves Go→Java; member CRUD rebuilt in Spring; existing rows backfilled into a default org per current owner.
- New cross-service call: user-service → dada-cloud `POST /internal/projects` on project create (failure handling + idempotency required).
- Role-change latency bounded by token lifetime.
- Two-service deploy coordination for any membership/claims change.

## Migration notes

1. Add `organizations`, `org_members`, `projects` (identity), `project_members`, `service_accounts`, `invitations` to user-service; backfill one org per existing project owner.
2. Extend key exchange to build fat claims (org_id, org_role, project map, scopes) via Keycloak protocol mapper or impersonatedLogin attributes.
3. dada-cloud: delete `getUserProjectRole`/local-mode roles; read all roles/scopes from claims; add `POST /internal/projects` provisioner.
4. Rewrite role enum + `project_members.role` values to the new 4; update `permissions.go`, `models/user.go`, `rbac.ts`.
