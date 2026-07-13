# First-use issue sweep (10 issues) — RESULTS

Source: user dialog + screenshots, 2026-07-14. "10 косяков за 5 минут первого использования."
All root-caused with live evidence; fixes committed. Source tags: [code]=source, [live]=prod.

| # | Issue | Root cause | Fix | Prod-verified |
|---|-------|-----------|-----|---------------|
| 1 | Members "IAM resource not found" | **rabbitmq scaled to 0 in git** → user-service IAM provisioning POST publishes to broker synchronously → blocks → console group-sync times out → group never created | argo-infra `eeb451a` replicaCount 0→1 (+ dada-cloud self-heal db9c2bf) | YES — rabbitmq 1/1 Running, user-service reconnected, NoRouteToHost flood→0; top.decker group already provisioned 18:49 [live] |
| 2 | Create-app validation only on submit | native `pattern` bubble + backend roundtrip, no live validation | 3943c44 live inline validation | deployed (img 3943c446); logic via tsc/eslint + regex parity |
| 3 | Create-app Kubernetes ban-word + English errors | label leaked "Kubernetes"; raw EN backend errors | 3943c44 reword + localize | deployed |
| 4 | Operations shoved on user post-create | post-create `router.push(.../operations)` | b9c4476 reroute apps→detail(poll), models/app-servers→list | CI deploying |
| 5 | Config card values.yaml/common jargon | i18n leaked helm internals | b9c4476 reword + strip (name)/(tag)/useDotEnv/(requests/limits) | CI deploying |
| 6 | Cost card "кластера" jargon | cost.note wording | b9c4476 reword | CI deploying |
| 7 | Env-vars tab hardcoded English | component not wired to i18n, light-only | b9c4476 full ru/en + theme-aware | CI deploying |
| 8 | Cost card "Не удалось загрузить" | opencost cold → backend 502; card only hid 503 | 70c011f backend 503 + card hides 502/503 | YES — opencost now 200 with data [live] |
| 9 | Domain `myredis-c1e9e9-dada-tuda-ru` (dashes) | render read top-level summary_json.fqdn (empty) → fell back to dashed k8s name | 70c011f read spec.dns.fqdn | YES — DB: spec.dns.fqdn=`myredis-c1e9e9.dada-tuda.ru` (dots), top-level empty [live] |
| 10 | myredis stuck Processing 5+ min | snapshot-sync lag (transient prior pod) + detail loads once | 70c011f poll while phase non-terminal | YES — phase=Ready, pod Running, reconciler healthy 30s [live] |

## Commits
- dada-cloud: db9c2bf (IAM self-heal), 3943c44 (validation), b9c4476 (ops/jargon/env i18n), 70c011f (domain/cost/status)
- argo-infra: eeb451a (rabbitmq replicaCount 1)

## Deploy status
- LIVE now: #1 (rabbitmq via Argo selfHeal), #2/#3 (backend img 3943c446)
- CI building → auto-deploy (~15min): #4-#10 frontend/backend (b9c4476, 70c011f)
- Data/infra root causes for #8/#9/#10 verified live; code fix ships via CI.
