# Fleet-wide Yandex.Metrika visitor↔auth binding (`dada_uid` cookie)

## Goal

Bind an authenticated visitor to their Yandex.Metrika session (`ym setUserID`)
across the fleet by publishing the authenticated user's **internal, non-PII id**
into a **JS-readable** cookie at the auth edge. Static/edge frontends that do not
own an auth context read this cookie via the `ya-metrika-inject` snippet (dada-argo
`helm/common`) and call `setUserID` + `userParams`.

## The one canonical cookie

| Field | Value |
|---|---|
| Name | `dada_uid` (fleet-wide; matches helm `analytics.yandexMetrika.uidCookie`) |
| Value | internal non-public id only — OIDC `sub` (Keycloak UUID) / internal user id. **NEVER** email / username / display name (152-ФЗ: no PII to Yandex). |
| HttpOnly | `false` — JS must read it |
| Secure | `true` on https (omitted on `http://localhost` dev only) |
| SameSite | `Lax` |
| Path | `/` |
| Domain | `.dada-tuda.ru` on the apex (cross-subdomain reach); host-only elsewhere |
| Max-Age | 30 days |
| Cleared | on logout / unauthenticated |

The value is URL-encoded on write (`encodeURIComponent`) and the snippet
URL-decodes on read.

## Edge / auth component audit

Every browser-facing auth/edge component in the workspace, and what it does with
the cookie:

| # | Component | Repo / path | Verdict | Why |
|---|---|---|---|---|
| 1 | **Console SPA** (`OidcBridge` / `LocalAuthProvider`) | dada-cloud `frontend/lib/auth-provider.tsx` | **SETS-COOKIE (primary)** | The real browser auth-termination point for the product. Owns `sso.principal.sub` (non-PII Keycloak UUID). Publishes `dada_uid` on `.dada-tuda.ru`, so one login reaches every same-domain static frontend. `@dada/react-sso` still binds the console's *own* Metrika directly; the cookie is for the *other* frontends. |
| 2 | Telemetry gateway | dada-cloud `backend/cmd/gateway` | **N/A** | Device-facing OTLP ingest (`dmon_`/`sk-dada-` keys). No browser ever hits it, serves no HTML. The "stamps `org_id`/`monitoring_app`" pattern is telemetry-label stamping, not a browser response. |
| 3 | Console API backend | dada-cloud `backend/internal/api` | **PASS-THROUGH** | Stateless `Authorization: Bearer` JWT resource server. Sets no cookies; the SPA owns the session. `sub` is in the JWT if a cookie were ever needed here. |
| 4 | grafana-embed-gateway | dada-cloud `backend/internal/grafanaembed/proxy.go` | **N/A** | Sets `dada_grafana_embed`, an HMAC session token (not identity), for an admin iframe. Not a Metrika-instrumented product page; identity rides `X-Webauth-*` headers. |
| 5 | oauth2-proxy `sso.dada-tuda.ru` | argo-infra `.../apps/oauth2-proxy` | **PASS-THROUGH / future** | Central SSO in auth-only mode (`--upstream=static://202`), fronts only OpenSearch Dashboards (admin, no Metrika). No native non-HttpOnly id cookie; per-ingress nginx `configuration-snippet` is unsafe here (see note). If a Metrika-instrumented app is ever put behind it, emit `sub` as `X-Auth-Request-User` and publish the cookie then. |
| 6 | nginx ingresses (OpenSearch dashboards/es-write, vm-metrics, jenkins, nexus) | argo-infra | **N/A** | Admin UIs / write APIs / basic-auth. Not Metrika product pages. |
| 7 | Keycloak `id/auth.dada-tuda.ru` | argo-infra `.../apps/keycloak` | **N/A / downstream** | Identity provider. `sub` is already in the issued token; binding happens downstream at the SPA/app after login, not on the IdP login pages. |
| 8 | user-service (Spring) | cloud `user-service` | **N/A** | Stateless resource server (`SessionCreationPolicy.STATELESS`), no form/oauth2 login, no `Set-Cookie`, serves no static assets. Key-exchange + introspection only; not the token issuer. |
| 9 | dada-argo fleet JS apps (profi, dada-lending-server, task-decomposition-frontend, agent-orchestrator-ui, tg-miniapp) | dada-argo `helm/javascript/services/*` | **READS-COOKIE** | Consume `ya-metrika-inject`. Fleet-wide default `analytics.yandexMetrika.uidCookie: dada_uid` (set in `helm/common/values.yaml`) turns the reader on for any app that enables Metrika. |

### Why no nginx snippet at the ingress

The ingress-nginx controller has been operated with per-ingress `*-snippet`
annotations treated as unsafe — in-repo comments (OpenSearch, Nexus ingresses)
record that a `configuration-snippet` makes the Ingress un-admittable and hangs
the Argo sync. So the cookie is **not** injected via `add_header Set-Cookie` at
the ingress. It is set by the code that actually terminates auth (the SPA), on
the shared `.dada-tuda.ru` domain, which reaches every same-domain frontend
without touching nginx.

### Cross-domain limitation

The `.dada-tuda.ru` cookie only reaches frontends on that apex. Products on other
domains (e.g. `*.a2a-hub.pro`, `profi.ru`, Telegram mini-app) cannot receive it;
those bind at the app level via `@dada/react-sso` when they own an auth context,
or remain anonymous until they do.

## Implementation

- **dada-cloud** `frontend/lib/uid-cookie.ts` — `publishUid()` writes/clears the
  cookie with the attributes above. Wired in `frontend/lib/auth-provider.tsx`:
  OIDC mode publishes `sso.principal.sub`; local mode publishes `user.id` (both
  internal ids), synced across hydration/login/logout/cross-tab.
- **dada-argo** `helm/common/values.yaml` — default `analytics.yandexMetrika.uidCookie: dada_uid`.
  Backward-compatible: injection stays gated on `enabled` + `counterId`.

## Verification

- Reader: `helm template` of a JS app with Metrika enabled renders
  `YM_UID_COOKIE value: "dada_uid"`; the injected snippet reads it and calls
  `ym(id,"setUserID",uid)` + `ym(id,"userParams",{UserID:uid})`.
- Writer: in-browser, `publishUid(sub)` produces a JS-readable `dada_uid` whose
  value round-trips to the sub; `publishUid(null)` clears it.
- Security: value is `principal.sub` / `user.id` only — no email/username/display
  name path exists.

## Activation checklist per fleet app

1. Set `analytics.yandexMetrika.enabled: "true"` + a `counterId` (or `autoProvision`).
2. `uidCookie` already defaults to `dada_uid` — no per-app value needed.
3. Confirm the app is served on `*.dada-tuda.ru` so it receives the cookie.
4. Validate: authenticated pageload has `document.cookie` containing `dada_uid`
   (non-PII), and `?_ym_debug=1` shows the `setUserID` call reach.
