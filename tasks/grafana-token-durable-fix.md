# Durable fix: Grafana 401 after emptyDir pod restart

## Root cause
- Shared Grafana runs on emptyDir. Pod restart wipes the Grafana DB.
- The console backend auths to Grafana with a **service-account token** (`GRAFANA_API_TOKEN`).
  SA tokens live in the Grafana DB → wiped on restart → console gets `401 Unauthorized`.
- The alert-rule reconciler (ADR-011) re-asserts folders/rules, but it uses the **same dead token**,
  so it cannot self-recover after a wipe. Only a manual token re-mint fixes it → recurs every restart.

## Chosen fix (no PVC needed)
Switch backend Grafana auth from the DB-backed SA token to **env-backed admin basic-auth**.
- Grafana injects `GF_SECURITY_ADMIN_USER`/`GF_SECURITY_ADMIN_PASSWORD` from the chart Secret
  `kube-prometheus-stack-monitoring-grafana` (a k8s Secret, NOT the emptyDir DB).
- On every boot Grafana recreates the admin user from those env vars → admin basic-auth always valid,
  even on a fresh DB. The chart Secret is stable across pod restarts AND Argo syncs (grafana chart 11.x
  uses `lookup` to preserve the generated `admin-password`).
- Verified live: `Authorization: Basic` against `/api/folders` and `/api/v1/provisioning/alert-rules` → 200.

PVC (option 1) is downgraded to optional (persists dashboards/annotation history); still blocked by
longhorn over-provisioning; NOT required for the 401 fix.

## Changes
### backend (dada-cloud)
- [x] config.go: add `GrafanaAdminUser`/`GrafanaAdminPassword` (`GRAFANA_ADMIN_USER`/`GRAFANA_ADMIN_PASSWORD`).
- [x] grafana/client.go: add basic-auth (user/pass + `NewBasicAuth`); `do()` prefers basic-auth over Bearer.
- [x] handler.go: build basic-auth client when admin creds set, else fall back to token.
- [x] client unit test: basic-auth path sends `Authorization: Basic`.

### cluster (out-of-git, manual — same pattern as existing GRAFANA_API_TOKEN)
- [ ] Patch secret `dada-cloud-console-backend` (ns argocd-prod): add `GRAFANA_ADMIN_USER`,
      `GRAFANA_ADMIN_PASSWORD` from the grafana admin Secret. Rollout restart backend.
- [ ] Deploy new backend image (CI + tag pin, console-migration) so the basic-auth code runs.

## Verification
- [x] admin basic-auth works against live Grafana API.
- [ ] `kubectl -n monitoring delete pod <grafana>`; admin basic-auth still works on fresh pod (credential survives wipe).
- [ ] After new image: delete grafana pod → console shows no `GET /api/folders 401`; reconciler re-asserts rules.
