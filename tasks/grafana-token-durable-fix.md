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
- [x] Patch secret `dada-cloud-console-backend` (ns argocd-prod): add `GRAFANA_ADMIN_USER`,
      `GRAFANA_ADMIN_PASSWORD` from the grafana admin Secret (additive; old image ignores them).
- [ ] **PENDING (user):** Deploy new backend image (CI + tag pin, console-migration) so the
      basic-auth code runs. Until then console runs on the bridge token below.

### bridge (one-time, until new image deploys)
- [x] After the verification wipe, re-minted admin SA `dada-console` (id 2, login sa-1-dada-console)
      via basic-auth, patched `GRAFANA_API_TOKEN`, rolled backend. Console healthy on token (200, no 401).
      This bridge dies on the NEXT wipe — the new image removes the need for it permanently.

## Verification
- [x] admin basic-auth works against live Grafana API (`/api/folders`, `/api/v1/provisioning/alert-rules` → 200).
- [x] `kubectl -n monitoring delete pod <grafana>` → fresh pod, empty DB; admin basic-auth STILL returns 200
      (`/api/folders` + `/api/serviceaccounts/search`). **Credential survives the wipe.**
- [x] Backend restart on bridge token: logs clean (no error/warn/401); console token → `/api/folders` 200.
- [ ] **PENDING (user):** after new image, delete grafana pod → console shows no `GET /api/folders 401`
      with NO token re-mint (basic-auth carries it).
