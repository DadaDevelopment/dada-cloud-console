# Out-of-git secret patch: DadaAgent cloud-task integration

Same pattern as the GRAFANA_ADMIN / monitoring secrets (tasks/grafana-token-durable-fix.md):
the prod backend runs with `existingSecret: dada-cloud-console-backend` (ns `argocd-prod`),
so secret env vars are NOT in git — they are patched directly onto the live Secret and the
backend is rolled. The Helm chart provides the non-secret half (URLs + client id) via the
ConfigMap; this runbook provides the secret half.

## What the chart already wires (in git, no action)

ConfigMap `dada-cloud-console-config` (from `backend.env` defaults, helm/dada-cloud-console/values.yaml):

| env | value |
|-----|-------|
| `DADA_AGENT_BASE_URL` | `http://dada-agent-service.argocd-prod.svc.cluster.local:8080` |
| `KEYCLOAK_TOKEN_URL` | `https://id.dada-tuda.ru/realms/master/protocol/openid-connect/token` |
| `CLOUD_AGENT_CLIENT_ID` | `dada-cloud-backend` |
| `CLOUD_TASK_CALLBACK_URL` | `https://console.dada-tuda.ru/api/v1/webhooks/dadagent` |

(`METRIKA_OAUTH_TOKEN` is intentionally unset — the metrika-instrumentor skill resolves the
Metrika token itself inside the agent workspace; the backend resolver no longer reads it.)

## What the operator must patch (out-of-git, secret half)

Three keys onto Secret `dada-cloud-console-backend` (ns `argocd-prod`). All additive — an old
backend image ignores unknown keys, so patch is safe before the new image rolls.

| key | source |
|-----|--------|
| `CLOUD_AGENT_CLIENT_SECRET` | Keycloak SA client `dada-cloud-backend` → Credentials tab → client secret (realm `master`, https://id.dada-tuda.ru) |
| `GITHUB_APP_ID` | argocd-dada GitHub App → Settings → Developer settings → GitHub Apps → argocd-dada → **App ID** (numeric; per session memory this is `3500292` — CONFIRM against the App settings page) |
| `GITHUB_APP_PRIVATE_KEY` | argocd-dada GitHub App → same page → Private keys → **Generate a private key** → downloaded `*.pem` (PKCS1). Same App as the connect-repo flow (slug `argocd-dada`). Reuse the existing PEM if one is already stored, else generate a fresh key (old keys keep working until revoked). |

### Commands

```bash
NS=argocd-prod
SECRET=dada-cloud-console-backend

# 1. client secret + app id (string values)
kubectl -n "$NS" patch secret "$SECRET" --type merge -p '{
  "stringData": {
    "CLOUD_AGENT_CLIENT_SECRET": "<keycloak-sa-secret>",
    "GITHUB_APP_ID": "<numeric-app-id>"
  }
}'

# 2. PEM private key — use --from-file so the multi-line PEM keeps its newlines
#    (kubectl patch JSON with embedded \n is error-prone). Two-step: stage into a
#    temp key, then merge. Simplest: kubectl create ... --dry-run | kubectl apply,
#    but to stay additive on an existing Secret use `kubectl patch` with the file
#    base64-encoded into the binary `data` field:
kubectl -n "$NS" patch secret "$SECRET" --type merge -p "{
  \"data\": { \"GITHUB_APP_PRIVATE_KEY\": \"$(base64 -w0 < argocd-dada.private-key.pem)\" }
}"
# (macOS: use `base64 < file | tr -d '\n'` instead of `base64 -w0`.)

# 3. roll the backend so it re-reads env
kubectl -n "$NS" rollout restart deploy/dada-cloud-console-backend
kubectl -n "$NS" rollout status  deploy/dada-cloud-console-backend
```

## Verify

```bash
# keys present (values redacted)
kubectl -n argocd-prod get secret dada-cloud-console-backend -o json \
  | jq '.data | keys[] | select(test("CLOUD_AGENT_CLIENT_SECRET|GITHUB_APP_ID|GITHUB_APP_PRIVATE_KEY"))'

# end-to-end: fire a cloud task from the console app chip. Before the patch this
# returns 502 "failed to mint install token"; after it should 202 and the
# cloud_tasks row goes status=running.
```

## DEPLOYED + VERIFIED on prod (2026-06-27)

- Keycloak SA clients `dada-cloud-backend` + `dada-agent` created (ServiceIdentity → crossplane openidclient, realm master). Secrets in `argocd-prod/<name>-keycloak` key `attribute.client_secret`. Both mint client-credentials tokens (azp correct).
- Backend image bumped `d9fd4233`→`22445a1f` (build #216, main HEAD). Live, healthy, log `cloud-task: dadagent webhook enabled`. ConfigMap has all 4 cloud-task env; Secret patched with the 3 keys (App ID `3500292` + PEM reused from `dada-cloud-console-build-agent`, CLOUD_AGENT_CLIENT_SECRET from the dada-cloud-backend client).
- DadaAgent rolled `master-1.0.0-209`→`211` (HEAD 55bbbf0) — includes skill vendoring (dd288e7) + Keycloak host reconcile (55bbbf0). Reachable + ready.
- **GitHub seam (the original 502) proven live:** App JWT (App ID 3500292 + PEM) → GET /app/installations OK (inst `126992982`, DadaDevelopment) → install-token mint OK (`ghs_…`, contents:write).
- **Inbound webhook proven live:** POST /api/v1/webhooks/dadagent — no auth → 401; `dada-agent` SA bearer → 200 `{"ok":true}` (JWKS + azp + id.dada-tuda.ru host all validate).
- Backend SA can list `yandexmetrikacounters` (RBAC ok).

### Remaining blocker to a green PR-producing fire (PRE-EXISTING, separate subsystem)
The create endpoint `POST …/cloud-tasks` will 424 at counter-resolve: `YandexMetrikaCounter` XR `status.counterId` is empty for all apps. Root cause = Crossplane composition `yandexmetrika-counter` (dada-argo) never patches the search DR response (`counters[0].id`) into `status.counterId`. The real id IS available — e.g. `dada-development-site` → `109705971` (Active) in the `*-ym-search` DisposableRequest response body. Fixed the stuck `crossplane.io/external-create-pending` annotation on that DR (it now reconciles Synced=True) but the composition mapping is the actual gap. Also the create call needs a console-user bearer (project canWrite) — not fabricated here; fire via the app-page chip (frontend on 22445a1f) or a browser token.

## Why each is needed

- `GITHUB_APP_ID` + `GITHUB_APP_PRIVATE_KEY` feed internal/github/installtoken.go (App JWT →
  install token). Missing either ⇒ firing a cloud task 502s "failed to mint install token".
- `CLOUD_AGENT_CLIENT_SECRET` feeds the Keycloak client-credentials token source
  (internal/dadagent) the backend uses to call the agent. Missing ⇒ agent submit 401s.
