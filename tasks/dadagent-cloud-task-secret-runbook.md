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

## Why each is needed

- `GITHUB_APP_ID` + `GITHUB_APP_PRIVATE_KEY` feed internal/github/installtoken.go (App JWT →
  install token). Missing either ⇒ firing a cloud task 502s "failed to mint install token".
- `CLOUD_AGENT_CLIENT_SECRET` feeds the Keycloak client-credentials token source
  (internal/dadagent) the backend uses to call the agent. Missing ⇒ agent submit 401s.
