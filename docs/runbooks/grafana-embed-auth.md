# Runbook — Grafana embed auth (backend-mediated, no manual login)

## What this is
The console iframes Grafana dashboards (monitoring/[appId] Dashboard tab). Embed
auth is **backend-mediated**: the console mints a short-lived HMAC token, an
**embed gateway** in front of `grafana.dada-tuda.ru` verifies it and injects
Grafana `auth.proxy` identity headers. No manual Grafana login; admins keep using
`grafana.dada-tuda.ru` directly (un-tokened traffic passes straight through).

```
iframe  https://grafana.dada-tuda.ru/d/<uid>?kiosk&theme=light&embed_token=<jwt>
  -> grafana.dada-tuda.ru Ingress (now owned by grafana-embed-gateway app)
  -> grafana-embed-gateway  (verify token -> set first-party cookie -> inject
                             X-WEBAUTH-USER / X-WEBAUTH-EMAIL)
  -> Grafana (auth.proxy enabled; whitelist = pod CIDR; per-USER folder ACL)
```

Isolation (Grafana **OSS** — no Enterprise Team Sync): the console grants each
requesting member `View` on their project's folder and strips the broad
Editor/Viewer role grants. A user can only render folders the console granted them.

## Components
- `dada-cloud` backend `cmd/grafana-embed-gateway` → image
  `ghcr.io/dadadevelopment/dada-cloud-console-grafana-embed-gateway` (Jenkinsfile).
- `dada-cloud` backend: `internal/grafanaembed` (token + proxy),
  `GetMonitoringGrafanaLink` mints the token + grants the user
  (`grafana.EnsureUserFolderAccess`).
- `argo-infra` `apps/grafana-embed-gateway` (Deployment/Service/Ingress).
- `argo-infra` `apps/kube-prometheus-stack/values.yaml`: `grafana.ingress.enabled=false`
  (gateway owns the host) + `grafana.ini [auth.proxy]`.

## Shared secret
One HMAC secret, identical in two places:
1. Gateway: Secret `grafana-embed-gateway` (key `embed-secret`) in ns `monitoring`.
2. Console backend: key `GRAFANA_EMBED_SECRET` in the backend env Secret.

Generate once: `openssl rand -hex 32`.

## Rollout (ORDER MATTERS — image before ingress flip)
The argo-infra change repoints `grafana.dada-tuda.ru` at the gateway. If the
gateway image is not yet built/pinned, the host goes down (ImagePullBackOff — the
build-agent trap). Do these in order:

1. **Merge dada-cloud** to a CI-push branch (main/develop) so Jenkins builds +
   pushes `dada-cloud-console-grafana-embed-gateway:<tag>`. Confirm the tag exists:
   `docker manifest inspect ghcr.io/dadadevelopment/dada-cloud-console-grafana-embed-gateway:<tag>`.

2. **Secrets** (out-of-git):
   ```
   SECRET=$(openssl rand -hex 32)
   # gateway
   kubectl -n monitoring create secret generic grafana-embed-gateway \
     --from-literal=embed-secret="$SECRET"
   # console backend (add key to the existing backend Secret, then restart)
   kubectl -n <console-ns> patch secret <backend-secret> --type merge \
     -p "{\"stringData\":{\"GRAFANA_EMBED_SECRET\":\"$SECRET\"}}"
   kubectl -n <console-ns> rollout restart deploy/<backend>
   ```

3. **ghcr pull secret in monitoring ns** (gateway image is private):
   ```
   kubectl -n monitoring get secret github-container-registry || \
   kubectl get secret github-container-registry -n <console-ns> -o yaml \
     | sed 's/namespace: .*/namespace: monitoring/' | kubectl apply -f -
   ```

4. **Pin the gateway image tag** in
   `apps/grafana-embed-gateway/chart/values.yaml` (`image.tag`) to the tag from #1.

5. **Push argo-infra `console-migration`** with: the new `grafana-embed-gateway`
   app, `kube-prometheus-stack` values (`grafana.ingress.enabled=false` +
   `auth.proxy`). Register the app if the platform App controller needs it.
   Argo syncs: gateway Deployment/Service/Ingress come up; the Grafana pod rolls
   to pick up `auth.proxy` (emptyDir — provisioning re-asserts on boot).

## Verification
1. **No manual login.** As a console user who has NEVER visited Grafana, open the
   monitoring Dashboard tab → panels render. Evidence:
   `GET https://grafana.dada-tuda.ru/api/ds/query` (in the iframe) returns 200, no
   redirect to `/login`.
2. **Cross-tenant denial.** As a user of project B, take project A's dashboard UID
   and load `https://grafana.dada-tuda.ru/d/<A-uid>?embed_token=<B's token>` →
   Grafana 403 / "Access denied to this dashboard" (B's user has no grant on A's
   folder). The token cannot carry A's identity (B cannot mint).
3. **Spoof check.** From a pod whose IP is NOT in `auth.proxy.whitelist`, hit the
   Grafana Service directly with a forged `X-WEBAUTH-USER` → rejected. (The public
   Ingress only reaches Grafana via the gateway, which strips client-supplied
   identity headers.)
4. **Admins unaffected.** `https://grafana.dada-tuda.ru` with no token → normal
   SSO/LDAP login works.

## Hardening (follow-up)
`auth.proxy.whitelist=10.244.0.0/16` trusts any cluster pod. Tighten with a
NetworkPolicy in ns `monitoring` allowing TCP:3000 to the Grafana pod ONLY from
the `grafana-embed-gateway` pods (plus Prometheus scrape), then narrow the
whitelist to the gateway pod IP range.

## Rollback
Set `grafana.ingress.enabled=true` in `kube-prometheus-stack` values and remove
the `grafana-embed-gateway` app + `auth.proxy` block; push. `grafana.dada-tuda.ru`
reverts to direct Grafana (manual login returns). Backend falls back to the plain
deep link automatically when `GRAFANA_EMBED_SECRET` is unset.
