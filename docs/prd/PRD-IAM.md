# PRD: Identity & Access Management (IAM)

## Status

Draft — 2026-06-21

Related: [ADR-009: IAM Ownership Split](../adr/ADR-009-iam-ownership-split.md)

## Summary

IAM is the foundation every other DADA Cloud product depends on (Monitoring API keys, Mobile Delivery service accounts, Deployments). We do **not** build authentication ourselves. Keycloak handles auth; the existing `user-service` (Java/Spring) and `gateway` own identity and API-key→JWT exchange. This PRD defines the **new** layer on top: organizations, project membership, project-scoped roles, service accounts, scoped API keys, and member invitations.

The single most important rule (the mistake every young platform makes): **do not write your own IAM**. Keycloak already solves it — orgs map to groups, roles to realm roles, scopes to client scopes. Our code only *orchestrates* Keycloak (Admin API on writes) and *decodes* its native claims downstream. No custom token enrichment, no mint-time RPC.

## Goals

- Users register, log in, recover passwords (Keycloak, already exists).
- Organizations group projects, members, billing, keys, and service accounts.
- Project-scoped membership and roles.
- Scoped API keys for Monitoring / Delivery / Deployments.
- Service accounts so Jenkins and agents work without a human login.
- Member invitations by email (+ add-existing shortcut).

## Non-Goals (MVP)

- SSO via Google/GitHub/Microsoft (V2 — Keycloak identity providers).
- Custom roles beyond the fixed four.
- SCIM provisioning.
- MFA configuration in console (delegated to Keycloak).

## Architecture (Keycloak-native RBAC)

See [ADR-009](../adr/ADR-009-iam-ownership-split.md) for full rationale.

- **Keycloak = system of record for identity + authorization.** Orgs, membership, roles, scopes live in Keycloak (groups + realm roles + client scopes) in the single shared realm. One copy of authz data — no sync, no mint-time enrichment call.
- **`user-service` (Java/Spring)** shrinks to: API keys, service accounts, key→JWT exchange, and **Keycloak Admin-API orchestration** (create org = create groups + role-mappings; add member = join a role-bearing subgroup). It does **not** own org/member/project tables.
- **`dada-cloud` (Go) = resource plane**: owns project/environment **resource** rows + org-keyed billing/quota, keyed by `project_id`/`org_id`. It authorizes by **decoding native Keycloak claims** (group paths + `scope`). The old `getUserProjectRole` / local-mode role logic is deleted.
- **`gateway`** exchanges API-key→JWT (existing Caffeine-cached flow). No claim injection — the minted token already carries native claims via stock mappers.

### Native RBAC model — group-encoded scoped roles

Four realm roles (`Owner/Admin/Developer/ReadOnly`), scope encoded in role-bearing subgroups:

```
/orgs/{orgId}/{Role}                        → org-level role
/orgs/{orgId}/projects/{projectId}/{Role}   → project-level role
```

Membership = authorization. Multi-org / multi-project = multiple group memberships on one user record. Org = group, **not** realm.

### Claims contract (emitted by stock Keycloak mappers — zero custom code)

```json
{
  "sub": "<principal_id>",                                  // user OR service account
  "groups": ["/orgs/acme/Admin", "/orgs/acme/projects/p1/Developer"],
  "realm_access": { "roles": ["Admin", "Developer"] },
  "scope": "read metrics:write logs:write deploy:write builds:read builds:write admin"
}
```

dada-cloud decodes `groups[]`: `/orgs/{org}/{Role}` → org role; `/orgs/{org}/projects/{proj}/{Role}` → project role. Effective project role = `max(orgRole(projectOrg), projectRole)` (org Owner/Admin cascade into every project; dada-cloud knows each project's org locally). Scopes read from the native `scope` claim. `/platform-admins` = staff god-mode, outside the enum.

## Domain model

### Roles (uniform, 4 values, both scopes)

`Owner`, `Admin`, `Developer`, `ReadOnly` — same enum at org level and project level. The legacy set (`platform-admin`, `developer`, `client-admin`, `client-viewer`) is removed everywhere (`models/user.go`, `permissions.go`, `rbac.ts`, migration rewrites `project_members.role`).

| Role | Org scope | Project scope |
|------|-----------|---------------|
| Owner | billing, delete org, everything | full control incl. delete |
| Admin | manage members/projects/keys/SAs | manage project, deploy, configure |
| Developer | create projects, see own projects | deploy, build, view logs/metrics |
| ReadOnly | view | view only |

**Internal staff override:** the existing hidden Keycloak group `/platform-admins` is kept as god-mode for support/ops. It is **never** a customer-assignable role and is not shown in UI. It lives outside the org/project role enum.

### Where state lives

- **Keycloak (system of record)**: orgs = `/orgs/{orgId}` groups; org/project roles = membership in role-bearing subgroups mapped to the four realm roles; users; the `/platform-admins` staff group. user-service mutates these via the Admin API — it keeps **no** org/member/project tables.
- **user-service (Postgres, kept)**:
  - `service_accounts`: `id, org_id, name, role, created_at` — a first-class non-human principal materialized as a Keycloak service-account user in the right subgroup. `api_keys.principal_id` references it (zero key-schema change).
  - `invitations`: `id, org_id, email, role, token, status (pending|accepted|expired), created_at, accepted_at`.
  - `api_keys` (exists): `id, principal_id, key_prefix, key_hash (SHA-256), created_at, last_used_at, revoked_at, expires_at`. Scopes are derived from the principal's role at exchange (via stock client scopes), not stored per-key for MVP.
- **dada-cloud (Postgres, kept)**: project/environment **resource** rows keyed by `project_id`/`org_id`; org-keyed billing/quota. No membership, no roles.

### Scopes vocabulary

`read`, `metrics:write`, `logs:write`, `deploy:write`, `builds:read`, `builds:write`, `admin`.

- Human session JWTs: scopes derived from role (Owner/Admin → all; Developer → write-capable minus `admin`; ReadOnly → `read`).
- API keys: scopes chosen explicitly at creation.
- **Enforcement is downstream**: each service guards its own routes (`requireScope("metrics:write")`) from the claim. Gateway stays a dumb router; it does not know per-route scope maps.

## Key flows

### Org / project creation

1. User calls user-service `POST /orgs` or `POST /orgs/{org}/projects`.
2. user-service Admin-API: creates `/orgs/{orgId}` (or `/orgs/{orgId}/projects/{projectId}`) + the four role subgroups, maps each to its realm role, joins the creator to `Owner`. Idempotent / reconciled on partial failure.
3. For projects, user-service then fires `POST /internal/projects` to dada-cloud → provisions the resource row + default environment.
4. Authorization works on the user's next token (group membership now exists; stock mappers emit it).

### API-key → JWT exchange (existing gateway flow)

1. Client sends `Authorization: Bearer sk-dada-...`.
2. Gateway calls user-service `/v1/apikeys/exchange` (Caffeine-cached 5 min).
3. user-service hashes key, resolves the principal (user or SA), mints a Keycloak token via `impersonatedLogin`. **Claims are filled by stock mappers** from the principal's group memberships — no per-key claim building.
4. Gateway swaps header to `Bearer <jwt>`; downstream decodes `groups[]` + `scope`.

### Service account (Jenkins)

- Admin creates SA `jenkins-ci` with role `Developer` in the org.
- Issues a scoped key (`builds:write`, `deploy:write`).
- Jenkins uses the key; gateway exchange yields fat claims exactly like a user. Rotating the key does not change *who* Jenkins is. Audit shows `jenkins-ci`, not a key id.

### Member invitation

- Admin invites by email → user-service creates pending `invitations` row + emailed token link (shared SMTP with Monitoring alerts).
- Invitee accepts → registers in Keycloak if new (or links existing) → becomes org member.
- **Add-existing shortcut**: Admin can add an already-registered user by email instantly (no token), for fast MVP onboarding.
- Pending invite for a non-existent user = a row that resolves on their first registration.

## API surface (new)

user-service:
- `POST /orgs`, `GET /orgs/{id}`, `GET /orgs/{id}/members`, `POST /orgs/{id}/members` (add existing), `DELETE /orgs/{id}/members/{principal}`
- `POST /orgs/{id}/invitations`, `POST /invitations/accept`
- `POST /orgs/{id}/projects`, `GET /orgs/{id}/projects`
- `GET/POST/DELETE /projects/{id}/members`
- `POST /orgs/{id}/service-accounts`, `GET /orgs/{id}/service-accounts`
- API keys: existing `POST/GET/DELETE /v1/apikeys` (extended with org/project + scopes)

dada-cloud (internal, called by user-service):
- `POST /internal/projects` (provision resource row + default env)

## UI (console)

- Org switcher (top nav).
- Members page: list members + roles, invite by email, add existing, change role, remove.
- Service accounts page: create/list/revoke SA + its keys.
- API keys page: create scoped key (plaintext shown once), list (prefix only), revoke.
- Project settings: project-level members + roles.

## Security

- API keys stored SHA-256 (existing). Plaintext shown once at creation only.
- Scope enforcement downstream from signed claims.
- Org isolation enforced on every resource query (`org_id` from claim must match resource owner).
- Invitation tokens single-use, expiring.

## Open questions

- Audit log of identity events (member add/remove, key create/revoke) — recommended, scope TBD.
- Billing tier on org — out of scope for this PRD, but org is the home for it.

## Success criteria

- A new user can register, create an org, create a project, invite a teammate, and assign a role — all through the console.
- Jenkins builds run under a service account key with no human login.
- A Monitoring API key with `metrics:write` can push metrics and nothing else.
- dada-cloud contains zero role-resolution DB logic; it authorizes purely from claims.
