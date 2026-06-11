# DADA Cloud — Reflective MCP Server: Design

**Date:** 2026-06-11
**Status:** Design approved (brainstorm complete) → ready for implementation planning
**Author:** brainstorm session (caveman mode)

---

## Understanding Summary

- Build a new **Go MCP server** (`mcp-server/`) in the dada-cloud monorepo exposing CRUD + special-action tools over all platform resources: ServiceDatabaseV2, App, PublicApi, AIModel, AppServer (VM), Projects, Operations.
- Tools are **auto-generated from the backend's OpenAPI spec** (swaggo-annotated Go handlers). No hand-written tool per resource. "Special settings not doable by plain POST" (promote, canary, image update, mlflow-pin, state, namespace-policy, retry, approve) surface automatically as their own tools from the sub-resource routes.
- MCP **wraps the existing `/api/v1` backend** over HTTP — zero business-logic duplication; reuses validation, RBAC, operation-queue, gitops path.
- **Async** model: mutating tools return the operation id immediately; a `get_operation` tool polls. Non-blocking, honest to the gitops reconcile lifecycle.
- **Auth**: OAuth 2.1 / Keycloak end-to-end, aligned to the approved **DADA SSO platform contract**.
- **Transport**: remote Streamable HTTP (hosted, e.g. `mcp.dada-tuda.ru`), multi-user.

### Why
Let users and AI agents order & manage DB/VM/app/model programmatically, with one reflective codebase that tracks the API automatically instead of N hand-written integrations.

### Who
Platform users/operators and AI agents acting as real Keycloak identities.

---

## Existing Estate (grounding — verified in repo)

- **Backend**: Go 1.22 + Gin + pgx/PostgreSQL. Routes hand-registered in `backend/internal/api/router.go`. No OpenAPI today. Auth = legacy local HS256 JWT (`backend/internal/auth/`).
- **Create path**: REST `POST /api/v1/...` → inserts row in `operations` table (status `Created`) → `gitops-agent` worker polls → renders K8s YAML → commits to git → ArgoCD syncs → platform controllers reconcile CRDs on `beget-prod`. Status cached in `resource_snapshots`.
- **Resources**: ServiceDatabaseV2 (**blocked** — V2 composition non-functional), App, PublicApi, AIModel, AppServer(VM).
- **Frontend**: Next.js 16 + React 19. Legacy local-JWT login in `localStorage`.
- **Approved SSO platform spec**: `python.metrics-lib/docs/superpowers/specs/2026-06-07-dada-sso-platform-design.md`.
  - Keycloak `id.dada-tuda.ru`, realm **`master`**, issuer `https://id.dada-tuda.ru/realms/master`, access-token TTL 4h.
  - oauth2-proxy `sso.dada-tuda.ru`, forward-auth, cookie domain `.dada-tuda.ru`, injects `X-Auth-Request-User/Email/Groups`.
  - Shared `Principal` contract; modes `proxy | bearer | auto`.
  - Bearer validation: RS256 JWKS, check `iss`+`exp`, `aud` optional (`VERIFY_AUD=false`, aud often `account`). Roles = `realm_access.roles` + `resource_access.service-client.roles`. Group `dada-tuda-users`.
  - Reusable libs: `dada.sso` (Python, in `python.metrics-lib`) + `@dada/react-sso` (= `react.dada-sso`).
- **Prior art**: `mcp-for-argocd` (separate repo, TypeScript). Reference only; our server is Go for monorepo reuse.

---

## Assumptions

1. id.dada-tuda.ru = Keycloak, realm `master`, standard OIDC discovery + JWKS. **Confirmed** by user + SSO spec.
2. swaggo can annotate the ~30 existing Gin handlers without restructuring them.
3. Official Go MCP SDK (`modelcontextprotocol/go-sdk`) supports Streamable HTTP + acting as an OAuth 2.1 resource server. **Verify at impl start.**
4. ServiceDatabaseV2 stays blocked; its tool generates but creates fail downstream — surfaced via operation status, not hidden.
5. Keycloak client/audience provisioning (clients `dada-console`, `dada-backend`/`service-client`, `dada-mcp`) is handled by **argo-infra gitops**, out of scope for this repo. We document the required config.

---

## Decisions (Decision Log)

| # | Decision | Alternatives considered | Why |
|---|----------|-------------------------|-----|
| D1 | MCP **wraps backend REST `/api/v1`** | Direct DB+gitops; direct K8s/Crossplane | Zero business-logic duplication; one code path; reuses validation/RBAC/operation-queue/gitops. |
| D2 | Tools **auto-generated from OpenAPI** | Hand-written tools | Reflective, can't drift from API, tiny codebase. |
| D3 | OpenAPI produced via **swaggo annotations** on Go handlers | Hand-written openapi.yaml; reflect Gin route table | Idiomatic Go, spec lives next to code, regenerates in CI. |
| D4 | **OAuth/Keycloak end-to-end**; backend **migrates off local JWT** | Token-exchange to backend JWT; personal access tokens | One identity everywhere; aligns with approved SSO platform direction. |
| D5 | MCP server in **Go, in monorepo** | TypeScript; Python | Reuse backend config/spec/OIDC patterns; one language/CI/deploy. |
| D6 | **Remote Streamable HTTP** transport | stdio only; both | Matches multi-user hosted platform + OAuth 2.1 model. |
| D7 | Async: **return operation id + `get_operation` poll** | block-until-settled; optional wait flag | Non-blocking, honest to gitops reconcile; no long-held requests. |
| D8 | Reflection: **hybrid — runtime engine + thin `overrides.yaml`** | pure runtime; build-time codegen | Reflective core + curated UX (names/grouping/visibility) for agent ergonomics. |
| D9 | Backend auth **`auto` mode** (proxy headers + bearer JWKS), mirroring `dada.sso` contract in a Go `principal` pkg | bearer-only; proxy-only | One image serves console (behind oauth2-proxy) + programmatic/MCP (bearer). |
| D10 | Authz driven by **Keycloak groups/roles**; `project_members` deprecated | keep project_members; hybrid | User choice — centralize membership in Keycloak. **Large migration; flagged as risk.** |

---

## Final Design

### Components & layout
```
dada-cloud/
├─ backend/                      # existing Go/Gin API — gains:
│  ├─ (swaggo annotations on handlers)
│  ├─ docs/openapi.json          # generated by `swag init`, committed + embedded
│  └─ internal/auth/
│        principal.go            # Principal contract (mirror of dada.sso §3)
│        oidc.go                 # JWKS fetch+cache, iss/exp(+opt aud) validation
│        proxy.go                # X-Auth-Request-* trust (proxy mode)
│        resolver.go             # mode: proxy | bearer | auto
│
└─ mcp-server/                   # NEW Go module
   ├─ cmd/mcp/main.go            # boot: load spec, build tools, start HTTP MCP
   ├─ internal/reflect/
   │    spec.go                  # parse openapi.json (kin-openapi)
   │    toolgen.go               # operation → MCP tool (name, desc, inputSchema, hints)
   │    proxy.go                 # tool args → /api/v1 request, forward bearer
   ├─ internal/overrides/        # overrides.yaml loader (rename/group/hide/annotate)
   ├─ internal/auth/             # bearer-mode validation + Principal (shared contract)
   └─ overrides.yaml
```

### Reflection engine (openapi → tools)
- One MCP tool per OpenAPI operation.
- **name**: from `operationId` (swaggo `@ID`); fallback `{method}_{path}` slug; overridable.
- **inputSchema**: merge path params (required) + query params (optional) + requestBody schema (`$ref` inlined via kin-openapi) into one flat JSON-Schema object.
- **description**: from swaggo `summary`/`description` — **written for an agent reader**.
- **handler**: generic proxy — template path from args, remaining args → JSON body, set method, attach caller bearer, call backend. `202` → `{operation_id, status, hint:"poll get_operation"}`.
- **hints**: `readOnlyHint` (GET), `destructiveHint` (DELETE) derived from method.
- **overrides.yaml**: `rename`, `hide` (e.g. logs/metrics endpoints), `group`, `annotate`. Applied after generation.

### Auth (Keycloak, per SSO platform contract)
- Shared Go `Principal`: `sub, username, email, name, groups[], roles[], source, raw`.
- Backend mode `auto`: proxy headers when present (console behind oauth2-proxy), else JWKS Bearer.
- MCP mode `bearer`: validates Keycloak access token (RS256/JWKS, `iss`+`exp`, `aud` optional), extracts groups/roles, forwards same bearer to backend.
- MCP as OAuth 2.1 resource server: serves `/.well-known/oauth-protected-resource` advertising Keycloak AS; clients do Auth Code + PKCE against Keycloak directly.
- Authz: Keycloak groups/roles (D10). Replaces `project_members` (migration required).
- Frontend: adopt `@dada/react-sso` (`proxy` or `oidc` mode). Removes legacy local-JWT login.

### Async, errors, edge cases
- Mutations return operation id; `get_operation(projectId, operationId)` polls status (`Created→…→Ready/Failed`) + error summary + commit SHA.
- 4xx → tool `isError` with backend message verbatim (agent corrects/retries). 401 → re-auth. 403 → forbidden. 5xx/unreachable → transient retry.
- ServiceDatabaseV2: create proceeds, fails downstream; surfaced via operation status.
- Spec/endpoint drift, missing operationId (fallback + boot log), destructive ops flagged, pagination params passed through. MCP stateless; backend owns limits.

### Testing
- Backend: `swag init` in CI; golden test = every Gin route present in spec; OIDC validator table tests (valid/wrong-iss/expired/bad-sig/rotated-key via mock JWKS); auth integration (testcontainers Keycloak or mock JWKS).
- MCP: fixture openapi.json → golden assert generated tool set (names/schema/hints); override tests; proxy tests (httptest backend asserts method/path/body/bearer; 202→poll shape; 4xx→isError).
- E2E happy path: MCP vs stub backend → `create_database` returns op id → `get_operation` returns status. No cluster needed.
- Manual proof before "done": run MCP locally, point an MCP client at it, order a DB on dev backend, watch operation reach terminal state — capture evidence.

### Deploy
- MCP ships in the same Helm chart (Deployment + Service + Ingress `mcp.dada-tuda.ru`). `openapi.json` delivered via backend image or shared configmap so spec + API stay versioned together.

---

## Risks

- **R1 (high): D10 Keycloak-groups authz migration.** Deprecating `project_members` means modeling every project membership in Keycloak. Large, cross-cutting, blocks cutover. Consider phasing (start hybrid) even though target is full Keycloak.
- **R2 (high): local-JWT → Keycloak cutover** is coupled across backend + frontend + existing sessions. Needs a coordinated migration window.
- **R3 (med): ServiceDatabaseV2 blocked** — its create tool will fail until V2 composition fixed (out of scope).
- **R4 (med): tool quality depends on swaggo annotation quality.** Weak descriptions → poor agent UX. Mitigated by overrides + annotation review.
- **R5 (low): Go MCP SDK OAuth-resource-server maturity** — verify at impl start; fallback to manual `/.well-known` + middleware.

---

## Non-Goals (v1)
- No K8s status write-back beyond existing snapshots.
- No MCP-side caching or per-tool rate limiting.
- No GraphQL.
- ServiceDatabaseV2 composition fix.
- Keycloak client provisioning (owned by argo-infra).

---

## Open Items for Implementation Planning
- Confirm Go MCP SDK choice + OAuth resource-server support (R5).
- Decide D10 phasing: hybrid-first vs big-bang Keycloak groups.
- Backend handler operationId/description annotation pass (drives tool UX).
- overrides.yaml initial curation list (which endpoints become tools vs hidden).
