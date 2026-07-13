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

## Round 2 (second session) — corrections + durable follow-ups

Prod now runs img `52802b7` (pods ~8min old): #1-#10 fixes above are LIVE.

### #1 root cause CORRECTED — rabbitmq was a RED HERRING
The "rabbitmq scaled to 0" theory (above, and the warning comment in the rabbitmq
values.yaml) is STALE. user-service is now **broker-free**: its values.yaml
excludes `RabbitAutoConfiguration` + `MessagingConfiguration` ("persists inline
via DirectUserOperations"), and the console's `EnsureProjectGroups` is a
**synchronous HTTP POST** to user-service, NOT an AMQP publish
([userservice/client.go:43]). Verified live: rabbitmq up for 5+ min → 0
connections (`rabbitmqctl list_connections` empty); no pod floods NoRouteToHost.
So the broker is genuinely unused and correctly at 0.
- The REAL #1 fix is `db9c2bf` (HTTP self-heal of missing project groups), which
  is live. top-decker is a PERSONAL-org project (org_id `top.decker`); its group
  is provisioned on project access via the self-heal path.
- Both this session (646f602) AND the prior session (eeb451a) briefly set
  rabbitmq→1 on the same stale comment; both reverted. Final: argo-infra
  `71d7425` sets replicaCount 0 AND rewrites the misleading comment so nobody
  falls for it a third time.

### #2 durable fix (NEW — prior sweep only localized the message)
The prior sweep localized the duplicate-name error but left the underlying
GLOBAL per-env uniqueness (the "S3-bucket namespace" surprise the user called
out) and the cross-tenant project-name LEAK. Fixed for real:
- `1674e06` — error no longer discloses the owning project/env (cross-tenant leak).
- `5c850ee` — NEW apps get a project-scoped ArgoCD Application name (App CR
  `spec.argoName=<app>-<env>-<projhash>`, `renderer.ScopedArgoName`); tenant-apps
  ApplicationSet already consumes it with a legacy fallback so EXISTING apps are
  never renamed (stateful-safe); backend uniqueness relaxed global→per-project.
  See [[project_appset_name_collision]].

### #4/#5/#8 extensions (NEW — sites the prior sweep missed)
`2b54cec`:
- #4 removed the remaining `router.push(.../operations)` redirects the prior
  reroute missed: databases + storage create flows, hostnames detach, and the
  databases/models detail `gotoOp` (now refresh-in-place; delete flows go to the
  resource list).
- #5 more ban-words the prior pass left: "Kubernetes"/"k8s" name hints in DB
  create, git import, storage, models; "кластер" in cost.empty/delete-impact/
  databases host+external copy.
- #8 the cost card's OTHER failure path: `cost.go` `projectNamespaces` returned
  500 (only 502/503 were suppressed); now 503 + the card hides ANY cost error.

Round-2 commits: dada-cloud 1674e06, 5c850ee, 2b54cec; argo-infra 71d7425
(rabbitmq 0 + doc), 7dce215 (argoName contract doc).
