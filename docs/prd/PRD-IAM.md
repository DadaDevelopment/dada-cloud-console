# PRD: Identity & Access Management (IAM)

## Status

Draft — 2026-06-21

Related: [ADR-009: IAM Ownership Split](../adr/ADR-009-iam-ownership-split.md)

## Summary

IAM is the foundation every other DADA Cloud product depends on (Monitoring API keys, Mobile Delivery service accounts, Deployments). We do **not** build authentication ourselves. Keycloak handles auth; the existing `user-service` (Java/Spring) and `gateway` own identity and API-key→JWT exchange. This PRD defines the **new** layer on top: organizations, project membership, project-scoped roles, service accounts, scoped API keys, and member invitations.

The single most important rule (the mistake every young platform makes): **do not write your own IAM**. Keycloak + `user-service` already solve 90%. Our code only does orgs, project binding, and roles.

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

## Architecture (authority split)

See [ADR-009](../adr/ADR-009-iam-ownership-split.md) for full rationale.

- **`user-service` (Java/Spring) = single authority** for: users, organizations, all membership (org + project), roles, API keys, service accounts, Keycloak provisioning. It **mints** projects.
- **`dada-cloud` (Go) = resource plane**: owns project/environment **resource** rows (k8s, deploys, apps, monitoring) keyed by `project_id`. It reads org + project roles from **fat JWT claims** only. The old `getUserProjectRole` / local-mode role logic is deleted.
- **`gateway`** exchanges API-key→JWT (existing Caffeine-cached flow) and now injects fat claims.

### Fat JWT claims contract

Every authenticated request carries (minted by user-service at login / key exchange, surfaced through Keycloak token):

```json
{
  "sub": "<principal_id>",          // user OR service account
  "org_id": "<org>",
  "org_role": "Owner|Admin|Developer|ReadOnly",
  "projects": { "<project_id>": "Owner|Admin|Developer|ReadOnly" },
  "scopes": ["read", "metrics:write", "logs:write", "deploy:write", "builds:read", "builds:write", "admin"]
}
```

dada-cloud authorizes purely from claims. Effective project role = `max(org_role, projects[project_id])` (org Owner/Admin cascade into every project).

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

### Entities (new, in `user-service`)

- `organizations`: `id, name, slug, created_at`.
- `org_members`: `(org_id, principal_id) unique, role`.
- **Project identity + membership moves to user-service.** `projects` (identity): `id, org_id, slug, display_name, created_at`. `project_members`: `(project_id, principal_id) unique, role`. (dada-cloud keeps only the *resource* row keyed by the same `project_id`.)
- `service_accounts`: `id, org_id, name, role, created_at`. A first-class non-human principal. `api_keys.principal_id` already references a principal — SA slots in with zero key-schema change.
- `invitations`: `id, org_id, email, role, token, status (pending|accepted|expired), created_at, accepted_at`.
- `api_keys` (exists): `id, principal_id, key_prefix, key_hash (SHA-256), scopes, created_at, last_used_at, revoked_at, expires_at`. Now carries enforced `scopes`; `org_id`/`project_id` association added so the key resolves to fat claims at exchange.

### Scopes vocabulary

`read`, `metrics:write`, `logs:write`, `deploy:write`, `builds:read`, `builds:write`, `admin`.

- Human session JWTs: scopes derived from role (Owner/Admin → all; Developer → write-capable minus `admin`; ReadOnly → `read`).
- API keys: scopes chosen explicitly at creation.
- **Enforcement is downstream**: each service guards its own routes (`requireScope("metrics:write")`) from the claim. Gateway stays a dumb router; it does not know per-route scope maps.

## Key flows

### Project creation

1. User calls user-service `POST /orgs/{org}/projects`.
2. user-service mints `project_id`, creates the project identity row + Owner membership (creator = Owner).
3. user-service fires `POST /internal/projects` to dada-cloud → dada-cloud provisions the resource row + default environment.
4. Fat claims work immediately (membership exists at mint time).

### API-key → JWT exchange (existing gateway flow, extended)

1. Client sends `Authorization: Bearer sk-dada-...`.
2. Gateway calls user-service `/v1/apikeys/exchange` (Caffeine-cached 5 min).
3. user-service hashes key, resolves principal (user or SA), builds **fat claims** (org_id, org_role, project map, scopes), mints Keycloak token via impersonatedLogin.
4. Gateway swaps header to `Bearer <jwt>`; downstream reads claims.

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
