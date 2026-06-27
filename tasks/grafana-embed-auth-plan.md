# Grafana embed auth — backend-mediated (auth.proxy) plan

## Problem
Console iframes `https://grafana.dada-tuda.ru/d/<uid>?kiosk`. Grafana auth = OIDC+LDAP,
no anonymous, `oauth_auto_login=false`. Iframe → Grafana `/login` page → blank until the
user manually logs into Grafana. Bug: "вход в графану не должен выполняться вручную".

## Decision (user-confirmed)
- Threat today: trusted staff only (LDAP/Keycloak master realm), no external tenants yet.
- Strategy: **Full backend-mediated auth** — console backend reverse-proxies Grafana,
  injects identity via `auth.proxy` (`X-WEBAUTH-USER`), Grafana enforces per-project
  folder ACLs by team. Hard cross-tenant denial inside Grafana itself.

## Architecture
```
browser iframe  ->  console.dada-tuda.ru/grafana/d/<uid>?kiosk&embed_token=<jwt>
                      |
                console backend reverse proxy  (Go, has Grafana admin creds + DB)
                  1. validate embed_token (HMAC, ~2m TTL, scoped to userID+dashUID)
                  2. on first hit: Set-Cookie embed_session (HttpOnly, Secure,
                     SameSite=None, path=/grafana)  -> first-party (same origin as console)
                  3. inject  X-WEBAUTH-USER:<console username>
                             X-WEBAUTH-EMAIL:<email>
                             X-WEBAUTH-GROUPS:proj:<projectUUID>,...
                  4. proxy ->  kube-prometheus-stack-monitoring-grafana.monitoring.svc
                      |
                  Grafana (auth.proxy enabled, whitelist=backend pod CIDR)
                    - auto_sign_up user from header
                    - team-sync groups -> per-project team
                    - folder ACL grants project team Viewer; broad roles stripped
```
Why token-in-URL THEN cookie: iframe GET can't carry a bearer; the SPA's later
`/grafana/api/*` sub-requests carry no token either. First validated request mints a
first-party `/grafana`-scoped cookie; everything after rides the cookie. Serving Grafana
under the **console origin** makes that cookie first-party → immune to 3rd-party-cookie death.

## Phases

### Phase 0 — immediate bug relief (1 line, ship first)
- argo-infra grafana.ini `[auth] oauth_auto_login = true`. Iframe silently completes
  Keycloak OAuth using the console user's existing master-realm session. Removes the
  manual login NOW for trusted staff. (Isolation still soft until Phase 2; acceptable today.)

### Phase 1 — backend reverse proxy + signed embed token
- `backend/internal/grafanaproxy/` : httputil.ReverseProxy to internal Grafana svc,
  header injection, embed-cookie mint/verify, embed-token (HMAC-SHA256) mint/verify.
- New config: `GRAFANA_INTERNAL_URL` (cluster svc), `GRAFANA_EMBED_SECRET`,
  `GRAFANA_EMBED_BASE` (=https://console.dada-tuda.ru/grafana).
- Route: `api.Any("/grafana/*path", h.GrafanaEmbedProxy)` on a NON-JWT group (auth via
  embed_token/cookie, not bearer). Mounted so console ingress forwards /grafana/* → backend.
- Change `GetMonitoringGrafanaLink`: return embed URL
  `<GRAFANA_EMBED_BASE>/d/<uid>?kiosk&theme=light&embed_token=<jwt>` instead of the raw
  grafana.dada-tuda.ru deep link. Token scoped to claims.Subject + dashboard UID, owner-checked.

### Phase 2 — Grafana isolation (teams + folder ACL) + auth.proxy config
- argo-infra grafana.ini: enable `[auth.proxy]` enabled=true, header_name=X-WEBAUTH-USER,
  auto_sign_up=true, headers="Email:X-WEBAUTH-EMAIL Groups:X-WEBAUTH-GROUPS",
  whitelist=<backend pod CIDR>, sync_ttl small. Keep allow_embedding/cookie_samesite.
- Grafana server: root_url=https://console.dada-tuda.ru/grafana/ + serve_from_sub_path=true.
  CONSEQUENCE: canonical Grafana URL moves to console.dada-tuda.ru/grafana; admin direct
  grafana.dada-tuda.ru SSO redirect breaks. Must confirm admin migration. (See open Q.)
- backend grafana client:
  - per-project Grafana team `proj:<uuid>` w/ external-group team-sync.
  - rewrite `SetFolderTenant` -> grant project team Viewer + strip broad Editor/Viewer role.
  - provision team in `ensureGrafanaResource`.

## Verification
1. Console user logged into console (never visited Grafana) opens Dashboard tab → panels
   render, zero manual login. Evidence: preview/network 200 on /grafana/api/ds/query.
2. Different-project user hits project A dashboard UID directly in Grafana under console
   origin → Grafana 403 (folder ACL by team). Evidence: curl with that user's X-WEBAUTH-GROUPS.
3. Spoof check: direct request to Grafana svc with forged X-WEBAUTH-USER from a
   non-whitelisted IP → rejected.

## Resolved decisions
- Admins keep grafana.dada-tuda.ru → NO root_url change. Gateway fronts the
  grafana HOST (not a console sub-path); un-tokened admin traffic passes through.
- Grafana is **OSS 12.4.0** (verified live): Team Sync (`/api/teams/{id}/groups`)
  404s → Enterprise-only. Isolation switched from team-sync to **per-user folder
  ACL**: backend grants each requesting member View on their folder, strips broad
  roles. Verified with unit tests (embed_acl_test.go).

## Results (this branch)
- backend: internal/grafanaembed (token+proxy, tested), grafana client
  EnsureUser/EnsureUserFolderAccess/SetFolderTenant (tested), GetMonitoringGrafanaLink
  mints token + grants user, config env vars, cmd/grafana-embed-gateway, Dockerfile,
  Jenkinsfile image. `go build ./... && go test ./...` GREEN.
- argo-infra (STAGED, not pushed — prod deploy, ordering matters): grafana-embed-gateway
  app+chart (lint+template OK), kube-prometheus-stack values (ingress off + auth.proxy).
- runbook: docs/runbooks/grafana-embed-auth.md (rollout order, verification, rollback).
- Rollout + on-cluster verification = user-triggered (see runbook). NOT YET DEPLOYED.

## Old open question (now answered: keep grafana.dada-tuda.ru)
- Phase 2 root_url change moves admin Grafana to console.dada-tuda.ru/grafana. OK to
  migrate, or must grafana.dada-tuda.ru stay the admin URL? (If must stay: run a second
  embed-only Grafana host/instance — larger.)
