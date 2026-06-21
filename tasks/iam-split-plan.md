# IAM Ownership Split — dada-cloud → pure claim-reading resource plane

Ref: docs/prd/PRD-IAM.md, docs/adr/ADR-009-iam-ownership-split.md
Decisions: local HS256 mode mints **dev-god** fat claims (Owner + all scopes). project_members **demoted** (kept, values rewritten), not dropped.

## New role enum
`Owner > Admin > Developer > ReadOnly`. Legacy map: platform-admin→Owner, client-admin→Admin, developer→Developer, client-viewer→ReadOnly.
`/platform-admins` KC group kept = internal god (→ Owner on all projects), outside enum, never in UI.

## Fat claims contract (JWT)
`org_id`, `org_role`, `projects` (map project_id→role), `scopes` ([]string).
Effective project role = max(org_role, projects[project_id]).
Scope vocab: read, metrics:write, logs:write, deploy:write, builds:read, builds:write, admin.

---

## Phase 1 — backend models + claims
- models/user.go: MemberRole consts → Owner/Admin/Developer/ReadOnly. Add RolePriority + MaxRole. Demote ProjectMember (comment).
- auth/jwt.go: add OrgID, OrgRole, Projects map[string]string, Scopes []string to Claims. Local GenerateToken mints dev-god claims.
- auth/oidc.go: parse org_id, org_role, projects, scopes into KeycloakClaims; keep groups for /platform-admins.
- router.go resolver: copy fat claims; /platform-admins ⇒ org_role=Owner.

## Phase 2 — authz from claims (delete DB role logic)
- permissions.go: DELETE getUserProjectRole, roleFromGroups, slugRolesFromGroups, rolePriority. Add effectiveProjectRole(claims, projectID)(role,ok), canWrite(new enum), requireScope(scope).
- Rewrite ~50 getUserProjectRole call sites → effectiveProjectRole; ErrNoRows → !ok (404).
- aimodels.go requireMember/requireWriter from claims.
- admin.go approval list + ApproveOperation gate from claims; no project_members JOIN.
- projects.go ListProjects from claims.Projects keys (+god=all); GetProject/SetNamespacePolicy via effectiveProjectRole.

## Phase 3 — scope middleware wiring
- builds GET→builds:read, write→builds:write; deployments→deploy:write; logs/metrics→read; admin/*→admin. Conservative.

## Phase 4 — internal provisioner
- config.go: InternalAuthToken (INTERNAL_AUTH_TOKEN).
- internal_provision.go: POST /internal/projects, requireInternalToken (X-Internal-Token). Body {project_id, org_id, slug, display_name, default_environment?}. INSERT explicit id, default env, idempotent. 201.
- router.go: /internal group outside /api/v1 auth.

## Phase 5 — migration
- migrations/016_iam_role_rewrite.sql: UPDATE project_members.role 4-way map; add projects.org_id (nullable); comment demoted. No DROP.

## Phase 6 — frontend
- lib/types.ts MemberRole new enum. lib/rbac.ts predicates rewritten.
- Fix refs: projects/page.tsx, models/[name]/page.tsx, admin/approvals/page.tsx.
- Members UI: org switcher + members + invite calling user-service (lib/userService.ts). Stub + TODO if endpoints absent.

## Phase 7 — verify + push
- go build/test (fix permissions_test.go, aimodels_test.go). frontend build. Push green.

## Contracts to coordinate (user-service chip)
POST /internal/projects: hdr X-Internal-Token; body {project_id,org_id,slug,display_name,default_environment}; 201 {project_id, default_environment_id}; idempotent on project_id.
JWT fat claims: org_id, org_role, projects{}, scopes[].

## Flagged out-of-scope
- gitops-agent UpsertProject (by-name) left intact; follow-up to route through /internal/projects.
