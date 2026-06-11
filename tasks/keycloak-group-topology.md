# Keycloak Project Authz Topology — Design

**Date:** 2026-06-11
**Status:** Design draft → needs 1 decision (provisioning model) before implementation
**Context:** M3 of `tasks/mcp-server-design.md`. User chose: model project authz as Keycloak groups, then deprecate the app-DB `project_members` table.
**Blocking finding:** Keycloak (realm `master`) today has **no project representation** — only group `dada-tuda-users` + realm role `user` (`argo-infra/.../keycloak-config/chart/templates/{groups,role}.yaml`). All per-project membership/roles live only in the app DB `project_members`. This design defines the missing topology + the migration off `project_members`.

---

## Goal
A user's Keycloak access token must carry enough to decide **per-project role** without an app-DB lookup, so authz reads come from the token (`Principal.groups`) and `project_members` can be retired.

Existing app roles to preserve: `platform-admin` (global), `developer`, `client-admin`, `client-viewer` (per project). Source: `backend/internal/models/user.go`.

---

## Decision Log

| # | Decision | Alternatives | Why |
|---|----------|--------------|-----|
| G1 | **Nested group per project+role**: `/projects/<slug>/<role>` (e.g. `/projects/acme/developer`). User's membership in that subgroup = their role on that project. | Realm roles per project (role explosion, no hierarchy); per-project Keycloak clients (absurd count); group attributes (not claim-friendly). | Keycloak's native hierarchy + Group Membership mapper emits full paths; one mapper covers all projects; clean parse. |
| G2 | **Global admin = top-level group** `/platform-admins` (realm role `platform-admin` as alias). | Derive from existing `admins`/`infra-administrators` groups oauth2-proxy already gates on. | Explicit, decoupled from infra-ops groups; can still also honor `infra-administrators` as super-admin. |
| G3 | **Token carries full group paths** via a Group Membership protocol mapper (`full.path=true`) on the console + MCP + service clients. Backend parses `/projects/<slug>/<role>` → map[slug]role. | Minimal `groups` (names only) — collides across projects. | Full paths disambiguate; standard Keycloak. |
| G4 | **Provisioning = app-driven via Keycloak Admin API** (see OPEN DECISION). Backend/gitops creates the `/projects/<slug>` group + role subgroups when a project is created, and adds/removes user memberships when members change. | Static Crossplane Group CRs in argo-infra (can't — projects are created dynamically at runtime). | Projects are dynamic; group lifecycle must track project lifecycle. |
| G5 | **`project_members` kept as a write-through cache during migration, then reads cut over to token groups, then table dropped.** | Big-bang drop. | Safe phased cutover; rollback possible until the final drop. |

---

## Topology

```
master realm
├─ dada-tuda-users            (existing — SSO access gate, unchanged)
├─ platform-admins            (NEW — global admin)            → realm role platform-admin
└─ projects                   (NEW — container)
   ├─ acme                    (one per project, slug = project.slug)
   │  ├─ client-viewer
   │  ├─ developer
   │  ├─ client-admin
   │  └─ (membership: user in exactly one role subgroup per project)
   └─ <slug>/...
```

**Claim mapping:** Group Membership mapper, `full.path=true`, claim name `groups`, on clients `dada-console` (SPA, PKCE), `dada-mcp` (SPA/public), and `service-client`. A user in `/projects/acme/developer` + `/platform-admins` emits:
`groups: ["/projects/acme/developer", "/platform-admins", "/dada-tuda-users"]`.

**Backend parse (`Principal` → authz):**
- `/platform-admins` (or realm role `platform-admin`) → global admin, bypasses per-project check.
- For each `/projects/<slug>/<role>` → `roleByProjectSlug[slug] = role`.
- `getUserProjectRole(projectID)` → resolve project slug → look up `roleByProjectSlug[slug]`. No DB hit.

---

## Provisioning integration (the real work)

Group lifecycle must mirror project + membership lifecycle:

- **Project create** → ensure groups `/projects/<slug>` + 4 role subgroups exist.
- **Project delete** → delete `/projects/<slug>` subtree.
- **Add/change/remove member** → set the user's membership in the right `/projects/<slug>/<role>` subgroup (remove from others).

Mechanism: **Keycloak Admin REST API** using `service-client`'s service account, which must be granted realm-management roles `manage-users`, `query-groups`, `view-users` (must be added in argo-infra — currently unknown if present). A thin Go `keycloak-admin` client (group CRUD + membership) called from:
- the backend member-management endpoints (when they exist), and/or
- the gitops-agent on project bootstrap (it already mirrors projects to git — natural home).

**User identity link:** still auto-provision a `users` row by `keycloak_sub` (FK integrity for `operations.actor_id`, `audit_events.actor_id` — both `REFERENCES users(id)`). Keycloak owns *authz*; the `users` row remains the local identity anchor for FKs. `project_members` is what gets retired, not `users`.

---

## Migration (phased, G5)

1. **Provision**: create `platform-admins` + `projects/*` groups; backfill — for every `project_members` row, ensure the matching KC group + membership (idempotent script via Admin API).
2. **Dual-write**: app member changes write to BOTH `project_members` and KC groups. Backend still reads authz from `project_members` (no behavior change yet). Verify token groups match DB for all users.
3. **Cutover reads**: backend `getUserProjectRole` reads from `Principal.groups` (token) instead of `project_members`. `project_members` now read-only cache.
4. **Drop**: remove `project_members` writes, then the table (and its FK from migrations) once stable.

Rollback is possible through step 3.

---

## DECIDED — provisioning trigger = **C. Crossplane Group CRs via gitops-agent**
gitops-agent renders Keycloak `Group` (and membership) CRs into the state repo on project/member operations; ArgoCD applies; provider-keycloak reconciles the groups in Keycloak. Fully declarative/gitops-native, consistent with how every other resource is provisioned. Cost: cross-repo write + slowest propagation (git→Argo→provider→KC→token refresh). G4 above is superseded by this (Admin-API path dropped in favor of CRs).

## Assumptions / prerequisites (argo-infra)
- Add `dada-console` (public PKCE, redirect `https://console.dada-tuda.ru/*` + localhost) and `dada-mcp` (public PKCE, audience for backend) clients + Group Membership mapper (`full.path=true`).
- Grant `service-client` service account realm-management roles for group/user management.
- Add `platform-admins` + `projects` parent groups (static), role subgroups created dynamically.
- These are Crossplane `Client`/`ProtocolMapper`/`Group` CRs in the keycloak-config chart — I can author the manifests; ArgoCD applies them.

## Risks
- **Dual source of truth during migration** (DB + KC) — mitigated by dual-write + verification step.
- **Admin API coupling** — backend/agent now depends on Keycloak availability for member ops (project create still works; member grant may queue/retry).
- **Token staleness** — role change requires token refresh to take effect (acceptable; 4h access-token TTL, or force re-login for sensitive demotions).
- **Group sprawl** — N projects × 4 subgroups; fine at platform scale (hundreds).
