# Public-route health (2026-08-04, reopened)

**Decision:** do not infer or surface a separate app type. A configured port is
the whole contract: a port creates a public HTTP expectation; no port means no
public route and no probe. The first port is auto-detected and every later
configuration change, including removal, is authoritative.

- [x] Trace and remove fallback paths that silently recreate a port/domain after it was removed.
- [x] Probe the real public URL (DNS, TLS, ingress/LB, HTTP), record a bounded result and expose a calm actionable state.
- [ ] Add metrics and alerting for both endpoint failures and watcher/LB failure; apply the LB fix from live evidence in its source repository.
- [x] Cover detect, remove-port, public failure, recovery, and no-port cases with unit/integration tests.
- [x] Verify the deployment path and document the result below.

## Review — Public-route health

- `port=0` is now preserved through archive detection, deploy handoff, GitOps rendering and Helm. The shared chart emits no Service, Ingress, default HTTP container port or HTTP probes in that mode.
- Public probing now targets the public HTTPS URL, not the in-cluster service. It writes `dada_public_route_probes_total{outcome=ok|edge_unavailable|bad_gateway}` and a compact per-app status with an actionable log link.
- Live evidence: 5 of 12 fresh TLS connections to `155.212.223.198` failed during handshake; successful connections reached the expected upstream 502. The public Service has `externalTrafficPolicy: Cluster`, two ready ingress pods on two of four nodes, and no health-check NodePort. The exact GitOps source for this Service is not present in the available repositories, so the required `externalTrafficPolicy: Local` change was not applied manually.
- Verification: backend API/metrics tests, gitops renderer/worker tests, build-agent DB tests, frontend typecheck/unit tests (80 passing), and Helm rendering for both service-enabled and service-disabled modes passed.

---

# VM App-Settings parity + full custom-domain wiring

**Goal:** the console App Settings page is k8s-only. For a VM (compose) environment it shows the wrong surface (empty helm config, ungated k8s mutations). Make every tab VM-aware and wire custom domains end-to-end for VM.

**Owner direction (2026-07-23):**
- Config (helm) -> this app's compose service slice (not the whole compose.yaml, not helm values)
- Storage -> this app's compose volume
- Resources -> removed for VM (k8s cpu/mem profile n/a)
- Domains -> nginx ingress + FULL custom-domain wiring (domain_hostnames -> compose ingress + TLS)
- Env -> keep (already works)
- Backend runtime guards -> yes, defense-in-depth

**Discriminator:** `environments.runtime IN ('k8s','vm')` (+ `app_server_id`). An "App" is a `resource_snapshots` row `kind='App'`; VM app summary_json = `{runtime:"compose", desired:{image,ports,volumes,compose}}`.

**Constraints:**
- Trunk-based on `main` (single release line, M4 n/a). Auto-push after each commit.
- Stage explicit paths, never `-A` (parallel worktrees p00b-work / worktree-agent-* exist). (M3)
- Frontend is a CUSTOMIZED Next.js — read `node_modules/next/dist/docs/` before writing Next code (frontend/AGENTS.md).
- New backend routes -> `swag init -o internal/api/docs` or TestOpenAPICoverage gates CI.
- No source comments (house rule); godoc/docstrings only.
- Phase 5 touches prod routing + TLS + git commits to user repos -> highest care, verify live.

---

## Phase 0 - Grounding (DONE)
- [x] Agent A: compose config model. KEY: ONE aggregate compose.yaml per ENV ({proj}-{env} Portainer stack); each App = one service block. Source of truth = DB snapshot `desired` (Image/Ports/Volumes; `desired.compose` map for ADOPTED) + `env_vars` table. Per-app `apps/<n>/compose.yaml` is LEGACY/superseded; the raw /compose WS editor edits DEAD files (save does NOT run renderEnvAggregate). Mutation template = `updateComposeAppImage` (dbwatcher.go:1548): patch snapshot.desired -> UpsertSnapshot -> renderEnvAggregate. UpdateAppEnvVarsPayload declared but NOT wired; env edits ride SetEnvVar (env_vars table) + take effect next deploy.
- [x] Agent B: domains/TLS. KEY: whole custom-domain path is k8s-hardwired. doAttachCustomHostname renders k8s Ingress into resources.values.yaml which renderEnvAggregate NEVER reads -> no-op on VM. VM nginx ingress (CreateIngress, ingress.go:55) takes hosts from user-typed payload; domain_hostnames unread; NO frontend caller, NO read/update endpoint (console only visualizes, read-only IngressDetail). DNS target = k8s LB (wrong for VM; need AppServer.VMIP). **TLS: ZERO automation for VM** — certs hand-placed on host /etc/letsencrypt, paths user-supplied. No ACME/certbot/cert-manager for VM.

## Phase 1 - Backend runtime guards (DONE + VERIFIED, uncommitted)
- [x] Helper: envRuntime + requireVM/requireK8s + valuesFile*ForRuntime (internal/api/runtime_guard.go NEW)
- [x] UpdateAppProfile: explicit 400 for VM (requireK8sRuntime) - apps.go
- [x] Reverse guards: RollbackApp / RestartApp / AdoptApp -> requireVMRuntime (400 for k8s) - apps.go
- [x] GetValuesToken: runtime-aware file gate (VM: compose.yaml/.env; k8s: values.yaml) - apps_values.go
- [x] (UpdateAppStorage: left k8s-guarded until Phase 4 repurposes it for VM)
- [x] Verified: gofmt clean, go build ./... 0, go vet 0, go test ./internal/api/... PASS (coverage gate ok)

## Phase 2 - Frontend per-runtime tab set
- [ ] settings/page.tsx: read selectedEnv.runtime; VM tab set = [Env, Config, Storage, Domains, Git], DROP Resources
- [ ] Surface the orphan /compose route (fold into Config tab for VM or link)
- [ ] tsc/lint/build green

## STATUS
- Phase 1 backend guards: PUSHED 4d1c409 (verified).
- Phase 3+4 BACKEND + gitops-agent: PUSHED 6b788d6 (verified: build/vet, swag coverage gate, go test api + gitops worker/renderer). New ops UpdateComposeConfig, UpdateComposeVolume. Worker patches desired -> renderEnvAggregate.
- Phase 2 + 3/4 FRONTEND: COMMITTED LOCAL 6328734, NOT PUSHED (verified: tsc/eslint/next build green, i18n ru+en, api contract match, port-regex widened for compose syntax). Editors read desired.compose.* ?? desired.*; bind-mount reject mirrors server.
- ALL PUSHED: owner authorized combined push; origin/main 6b788d6 -> d804a7d (my 6328734 + 10 concurrent dbmove commits). Jenkins triggered; NOT yet confirmed green (findJobsWithScmUrl errored, plugin ClassCastException; local verify was green).
- Phase 5 (domains, BYO TLS): SHIPPED 2be63f0 (pushed). VMIngressSpec.ExtraHosts serving vhost + BYO cert `/etc/nginx/certs/live/<host>/`; attach/detach worker VM-branch mutates ingress custom_hosts; doCreateIngress persists key_path+custom_hosts; recordTarget->VMIP for VM; frontend Domains tab re-added. REVIEW CATCH: rebuildIngressCompose REFUSES when ingress has basic_auth (path not persisted -> would silently drop password gate). NOT live-verified (needs real VM + operator cert). Inert until a domain is attached.
- ALL 5 PHASES SHIPPED. Jenkins builds NOT confirmed green (API errored). Live-VM verification is the remaining gate for Phase 3/4/5 behaviour.

## Phase 3 - Config tab = compose service-slice editor (VM)  [tractable]
- Read GET: app's summary_json.desired (Image/Ports/Volumes for authored; desired.compose map for adopted)
- Write: NEW op `UpdateComposeConfig` (backend handler copy RollbackApp + typed payload) -> gitops-agent NEW dispatch case + handler patching snapshot.desired -> UpsertSnapshot -> renderEnvAggregate (template = updateComposeAppImage dbwatcher.go:1548)
- Frontend: new structured editor mirroring CommonConfigEditor, mounted for VM in Config tab
- DEPLOY ORDER: gitops-agent handler must ship BEFORE frontend enqueues the op (else op claimed -> dispatch default -> fails). Land backend+agent, verify live, then frontend.

## Phase 4 - Storage tab = compose volume editor (VM)  [tractable]
- NEW op `UpdateComposeVolume` patching desired.volumes (per-service mount) / desired.stack_volumes -> renderEnvAggregate re-derives external pins (externalVolumesFor) — data-safe pin preserved
- Same deploy-order rule as Phase 3

## Phase 5 - Domains = full compose custom-domain wiring (VM)  [BLOCKED on owner decision]  [RISKIEST]
Whole path is k8s-hardwired; needs compose branches in doAttachCustomHostname / doDetach / doAttachDefaultDomain, DNS target -> AppServer.VMIP, wire domain_hostnames -> VM nginx ingress spec -> re-render nginx conf into ingress App desired.compose -> renderEnvAggregate. Plus a create/edit ingress endpoint+UI (none today).
- [ ] **BLOCKER: VM TLS issuance strategy** (no automation exists). Options: (a) BYO — user uploads/points to host cert; (b) certbot sidecar on VM (HTTP-01 through the nginx); (c) platform issues LE cert + pushes to VM. Owner must choose — prod routing + cert lifecycle + security.
- [ ] After decision: implement chosen TLS path + domain wiring + verify live (DNS + cert + 200 through VM nginx)

## Phase 6 - Verify + ship
- [ ] Backend: go build ./... + go test ./... green; swag init if new routes
- [ ] Frontend: build green
- [ ] Live smoke on the reported app (env 5bff9f95)
- [ ] Review section below

---

## Review
(to fill in after implementation)

---

## SEO weekly loop (2026-07-31)

Plan and evidence: `tasks/seo/PLAN.md`, baseline `tasks/seo/2026-07-30.md` / `.json`.

- [x] Pull real data (Webmaster v4 + Metrika stat v1), classify all 97 shows into intent clusters
- [x] 301 the five dead slugs (ru+en) in `next.config.ts`
- [x] Link the six orphan landings from the footer
- [x] Apex `dada-tuda.ru` cert + 301 to `cloud.dada-tuda.ru`
- [x] Stop serving 5xx to crawlers during a frontend roll (`URL_ALERT_5XX` now ABSENT)
- [x] Ship `/oplatit-vercel-iz-rossii` and `/rabotaet-li-vercel-v-rossii` (ru+en) for the payment-block cluster
- [x] Fix `FaqList` — answers were never in the DOM, so every landing read as thin to a crawler
- [x] Expand the ten sub-500-word pages past ~900 words of page-specific content
- [x] Submit the changed URLs to IndexNow once the deploy is live — 94 URLs, Yandex 202, Bing 200
- [x] Verify live after the roll: 94/94 sitemap URLs 200, new payment pages 1112-1482 words
- [ ] Watch, do not pre-expand: 32 thin `/developer/*` docs pages (442-668 words). Expand only if
      the weekly pull shows them indexed then dropped as `LOW_QUALITY`
- [x] **Owner did 07-31:** region set via Yandex Business UI — diagnostics not re-evaluated yet
      (`last_state_update` still 07-30T13:35), verdict deferred to the 08-06 pull
- [x] **Owner did 07-30:** Google Search Console verified, `/sitemap.xml` processed Успешно,
      88 URLs discovered. Real Googlebot started crawling 07-30 17:17 (ingress logs: `/` 200,
      `/analog-railway` 200, CSS chunk = rendering service). 0 clicks is not yet a signal — the
      Performance chart ends 07-28, before submission
- [x] Fix the apex redirect dropping the path (`dada-tuda.ru/robots.txt` -> homepage HTML);
      host-conditioned 301 in `next.config.ts`, verified locally
- [x] Repoint the apex ingress at the frontend service so that redirect takes effect
      (argo-infra `d4f2061d`) — verified live: apex `/robots.txt` now returns robots, not HTML
- [ ] **Only remaining Google lever: backlinks.** A fresh third-level domain with none will not
      rank no matter how many pages it has
- [ ] 2026-08-06: re-run `scripts/seo-weekly.py`, grade the three standing predictions in PLAN.md

## Review 2026-08-02 — Russian docs (069f0b0, 39eca8e)

**Diagnosis.** The docs were never missing from the index, so "add to sitemap" was
the wrong fix. `/developer/*` and `/en/developer/*` shipped byte-identical English
bodies; the Russian route just carried `lang="ru"` around English prose. Yandex
sees an English page for a Russian query and near-duplicate hosts for the pair.

**Done.**
- [x] 18 of 20 guides translated (`frontend/content/docs/ru/`); `mcp-tool-reference.md`
      stays English on purpose (machine reference), README is not a routed page
- [x] Split the two routes — each owns `generateStaticParams`/`generateMetadata`
      and renders the shared `DocArticle` with its own locale
- [x] Untranslated slug degrades to the English original with an explicit note,
      not a 404
- [x] Widened the MCP tool-count anti-rot guard to read Russian (`\p{Cyrillic}`,
      Go RE2 `\w` is ASCII-only) so a translated page cannot rot the count unwatched
- [x] Landing → doc cross-links: `/storage`, `/databases`, `/cloud-servers`,
      `/pricing` each now feed their guide
- [x] Verified live by `<title>`: `Объектное хранилище S3`, `Управляемый PostgreSQL`,
      `Рецепты MCP`; `/en/*` still `Object Storage (S3-compatible)`
- [x] 24 URLs to IndexNow (Yandex + Bing 200/202) — chunked 4 at a time after the
      full batch died on a TLS handshake timeout
- [x] Guard so it cannot silently regress: `npm run check:docs`, wired into
      `prebuild` so CI and `docker build` both run it

**Not done / next.**
- [ ] No Google lever here either — IndexNow does not reach it, and the backlink
      gap above is still the binding constraint
- [ ] 2026-08-06 pull: watch whether the RU doc pages enter the Yandex index at all.
      They were the 32 thin pages flagged above; they are no longer thin, but they
      are newly Russian, so treat 08-06 as their first honest measurement

---

## 2026-08-04 — Бокс-пул жёг 15.6% счёта: почему сборщик его не видел

**Повод.** Аналитик: бокс-пул держит 2 345 ₽/мес (15.6% счёта) при нулевом внешнем
спросе; 3 774 минуты за всё время, 96% — `suspended_disk`, все боксы наши. Владелец:
«я не просил сжигать кластер ради фичи, за которую никто пока не платит».

**Диагноз — не «фича дорогая», а один UPDATE.** `executeSuspendBox` писал
`status='Sleeping'`, но не ставил `slept_at`. `reapSleeping` фильтрует
`AND b.slept_at IS NOT NULL`. Значит КАЖДЫЙ бокс, уснувший через операцию — а это
ровно то, что делает сам репер по idle-таймауту и по TTL, — становился невидим для
сборщика навсегда и держал свой 10-20Gi том до конца времён, метря `suspended_disk`.
Отсюда и 96%: это не «пользователи спят», это боксы, которые некому было убрать.

Ещё три дефекта на том же пути, каждый ломал удаление сам по себе:
- ветка `asleep >= 72h` слала только ФИНАЛЬНОЕ письмо, если не было первого —
  `reap_warned_at` оставался NULL, ветка перезаходила вечно, удаления не случалось;
- `executeResumeBox` не чистил `reap_warned_at`/`reap_final_warned_at` — следующий
  сон начинался с обоими предупреждениями «уже потраченными», то есть удаление без
  единого письма;
- `main.go` заводил репер ВНУТРИ ветки успеха `NewBoxMeter`: нечитаемый
  `box-fleet-cost.yaml` останавливал не биллинг, а сбор мусора. Ровно наоборот к
  тому, что нужно флоту, за который никто не платит.

**Чего сборщик не умел вовсе.** `ReapOrphans` смотрел только на поды. Кристаллизация
оставляет Deployment/Service/Ingress/PVC `crystal-<vm>`, `expose` — Service+Ingress;
этих форм он структурно не видел. Новый проход `ReapUnclaimed` идёт от ПРАВДЫ БАЗЫ
к кластеру: всё, на что нет живой строки, — мусор.

**Сделано.**
- [x] `box_operations_sleep.go`: suspend ставит `slept_at`, resume чистит все три
      штампа (`slept_at`, оба `reap_*_warned_at`) и двигает `expires_at`
- [x] `box_reaper.go`: самолечение (`slept_at = updated_at` для уже застрявших строк)
      + правильный порядок «первое письмо → финальное → 6ч выдержки → удаление»
      (`boxReapGraceAfterFinalWarning`), чтобы бокс не удалялся через 4 минуты после
      письма «ваш бокс будет удалён»
- [x] `internal/box/clusterreap.go`: `ReapUnclaimed` — под, workspace-PVC, crystal
      Deployment/Service/Ingress/PVC, expose Service/Ingress. Grace 30 минут (спавн
      создаёт под раньше строки), `phase=parked` не трогается (это пул)
- [x] `internal/api/box_unclaimed.go`: `LiveObjects.Complete` — блокировка от
      «база недоступна → ничего не занято → снести весь флот». Postgres на этой
      платформе падал дважды за неделю, это не гипотетика
- [x] `main.go`: репер стартует независимо от метра
- [x] Тесты: 4 новых на `ReapUnclaimed` (fake clientset), 3 новых на штампы сна
      (real-DB), обновлён `RefusesToDeleteWithoutBothWarnings` под выдержку.
      Прогнано на живом postgres: `internal/api` и `internal/box` зелёные
- [x] `BOX_WARM_POOL_SIZE: 1 → 0` (пул ПОРЕПЛИЧНЫЙ, то есть это два тела и два
      тома круглосуточно ради ~15 секунд холодного старта, которых никто не ждёт)
- [x] **Выключателя пула не существовало.** `config.go` читал `BOX_WARM_POOL_SIZE`
      через `getEnvInt`, где `n <= 0` считается мусором и подменяется дефолтом
      **2**. То есть выставленный ноль означал «пул на два». Проверено на проде
      после выката `a81e594f`: ConfigMap `BOX_WARM_POOL_SIZE: "0"`, `env` внутри
      пода `BOX_WARM_POOL_SIZE=0` — и ровно в минуту старта новых подов в
      `dada-boxes` родилось второе тело с 10Gi. Values-файл, ConfigMap и env
      показывали одно, процесс делал другое, и счёт выглядел как авария кластера,
      а не как парсинг. Чинит `getEnvIntAllowZero` (ноль — значение, минус и
      опечатка по-прежнему падают в дефолт) + тест
      `TestBoxWarmPoolSizeZeroTurnsThePoolOff`. Триммить пул `Warm(...,0)` умел и
      до этого (`poolTrim`), спавн при пустом пуле идёт холодным стартом
      (`ClusterPool.coldStart`), а не `pool_exhausted`
- [x] Квота `dada-boxes-fleet` 6 подов/120Gi → 3/40Gi — потолок расхода, а не план
      мощности

- [x] Три наших спящих бокса удалены через продуктовый путь (`DELETE
      /api/v1/projects/{id}/boxes/{name}` токеном `dada-routine-svc`):
      `e2e-zero-0802`, `e2e-cryst-0802`, `m2-box-up-1`. В `dada-boxes` осталось
      ровно одно тело — парковочный под тёплого пула и его 10Gi. Ждать 72 часа
      писем самим себе смысла не было: 30Gi упирались в потолок квоты
      (`requests.storage 40Gi used = 40Gi hard`), то есть новый бокс было НЕ создать

**Красный CI по дороге (сборка #890).** Мои коммиты не доезжали до прода не из-за
кода: `#890` от 08-03 18:21Z собрал и запушил все образы, а на последнем шаге
(bump тега в argo-infra) потерял агентский под и 3 часа крутил
`node block ... neither running nor scheduled; cancelling`. `disableConcurrentBuilds()`
означает, что все следующие пуши висели в очереди за зомби (`why: Build #890 is
already in progress`). Вылечено `POST /890/stop` + `/term`; очередь сразу отдала
`#891` на `a81e594`, где мои коммиты уже внутри. Почему под исчез — событий за
18:34Z уже нет (ретенция), ноды сейчас чистые (`DiskPressure=False` на всех
четырёх); ГИПОТЕЗА: попал в то же окно, что и P0 с забитым диском под postgres
08-03 18:03Z. `#889` до этого падал на `npm ci` 503 от Nexus.

**Выкат и живая проверка (08-04 23:19Z).** Прод на образе `bb6e9335` (сборка `#895`,
пин argo-infra `1434c87`), внутри `a3b5061`. Лог бекенда:
`box: cluster warm pool trimmed back to target; surplus warm boxes were holding
fleet quota  available=0 changed=-2 target=0`. `dada-boxes` — `No resources found`,
`resourcequota dada-boxes-fleet` used = нули по всем позициям, включая
`requests.storage`. Флот бокса стоит ровно 0 ₽ до первого реального спавна.

**Красный CI по дороге (сборка #895).** Помечена FAILURE не из-за кода: под агента
`xvkps-jnvt1` умер на ноде `d5dns` с
`Failed to pull image "mcr.microsoft.com/playwright:v1.61.1-noble": no space left on
device` (забился containerd на ноде); за 39 минут до этого другой агент не влезал —
`0/4 nodes are available: 4 Insufficient memory`. Jenkins поднял второй под и прошёл
пайплайн заново; `runStage` без гардов по `currentBuild.result`, поэтому write-back
всё-таки записал пин, и выкат состоялся. Ноду не чистил — это мутация прода без
подтверждения; на момент проверки на `d5dns` уже было 25.5Gi свободно (fs 46.1/71.6Gi,
imageFs 15.7Gi). Осталось как риск: диск ноды 71.6Gi мал под агент с dind + playwright
+ node_modules + go-кеш, и следующий параллельный агент ляжет так же.

**Не сделано / дальше.**
- [ ] Проверить через сутки после выката: `box_usage` за день, доля
      `suspended_disk`, и что `dada-boxes` пуст, кроме живых боксов
- [ ] Владельцу: диск нод CI (71.6Gi) — либо растить, либо резать образ агента;
      сборка выживает только за счёт повторного запуска на другом поде

---

# App knows itself: снапшот-правда вместо эхо профилей (2026-08-04)

**Проблема (владелец):** «зайти в консоль и увидеть, что всё, что мне известно
про проект, ЗНАЕТ облако». Страница аппа `cloud-console` показывает `Реплики 10`
без ready, `Порт 8080` и `250m CPU · 256Mi` (выдуманные дефолты), «Репозиторий не
привязан» при наличии git_sha/автора/коммита в снапшоте, пустые логи и метрики,
домен `Pending` при живом 200.

**Корни (проверено на проде 08-04):**
1. `statusreconciler` знает реальные namespace/образы каждого workload'а, но
   пишет в снапшот только агрегаты (`replicas/ready/restarts/image`). Реальный
   namespace теряется.
2. `logs.go:k8sAppNamespaces` и `metrics.go:GetAppMetrics` берут
   `environments.namespace` (`platform-prod`), а поды живут в `argocd-prod` →
   0 строк логов и `no data`. В Prometheus серии ЕСТЬ:
   `container_memory_working_set_bytes{namespace="argocd-prod",image="...backend:a81e594f"}` = 2.
3. `FillEffectiveResources` подставляет профиль `small` (250m/256Mi) аппу, у
   которого профиля нет вообще — выдумка выдаётся за факт.
4. Фронт печатает `summary.replicas ?? 2` и `summary.port ?? 8080`, поля
   `ready`/`restarts` не читает.
5. Плитка «Репозиторий» читает только `repo_full_name`; `git_sha`+`git_message`
   игнорируются, из-за чего next-step зовёт «подключить репозиторий» у
   gitops-управляемого аппа.
6. Все `PublicApi` в кластере имеют `Ready=False` (`Unready resources:
   <n>-beget-dns-request`) → консоль вечно рисует `Pending` даже там, где домен
   отвечает 200.

## Шаги
- [x] 1. gitops-agent/statusreconciler: собирать `namespaces`, `images`,
      per-pod `observed_resources` (requests/limits первичного контейнера) и
      писать их в summary_json. Строго read-only по кластеру.
- [x] 2. backend/logs.go: namespace для инфра-потока = снапшотные `namespaces`
      ∪ namespace окружения.
- [x] 3. backend/metrics.go: k8s-запрос по снапшотным `namespaces`+`images`
      (regex-матчер), фоллбек на старую пару ns+image.
- [x] 4. backend/apps.go: `FillEffectiveResources` больше не выдумывает профиль
      там, где есть наблюдаемые ресурсы.
- [x] 5. frontend: «Реплики» = `ready/desired` + рестарты; «Порт» без
      выдуманного 8080; «Размер» из observed_resources; «Репозиторий» показывает
      коммит-источник для gitops-аппов.
- [x] 6. frontend/app-next-step: не звать «подключить репозиторий/домен» у
      gitops-управляемого аппа.
- [x] 7. Домен: не выдавать `Pending` от XR за вердикт (отдельный шаг, после 1-6).
- [x] 8. Verify: go build/vet/test, tsc/lint, и прогон правды на проде.

- [x] 9. backend/logsearch: инфра-логи матчились только по `dada.io/app`; у
      adopted-аппов (ADR-013) этого лейбла нет вообще — добавлен матч по
      `app.kubernetes.io/instance` и `argocd.argoproj.io/instance`.

## Результат (08-04)
- Проверено на живом OpenSearch: запрос новой формы по `argocd-prod` +
  `cloud-console` даёт 10000+ записей (было 0).
- Проверено на живом Prometheus: серии `container_*{namespace="argocd-prod",
  image=~"...backend:a81e594f"}` теперь попадают в scope метрик аппа.
- Домен: 52 из 53 `PublicApi` висят `Ready=False` из-за застрявшей аннотации
  `crossplane.io/external-create-pending` на composed provider-http `Request`
  (при `status.response.statusCode=200`). Прод НЕ трогал: консоль теперь сама
  резолвит запись (`dnsRecordLive`) и повышает Pending→Ready только по факту DNS.

---

# Logs viewer: layout and timestamp clarity (2026-08-04)

## Review

- [x] Long log messages/URLs wrap inside the message column without moving the
      timestamp and stream columns out of alignment.
- [x] API timestamps are rendered in the browser timezone (`HH:mm:ss`), which is
      the user's actual local time; no silent UTC override is applied.
- [x] Verified with frontend unit tests (80/80), targeted ESLint, and production
      Next.js build.

---

# Reactivation campaign: registered, never deployed (2026-08-06)

Cohort ground truth from prod (`cloud-console`, `user_accounts` view): 18
customer accounts, 10 of them with zero builds ever. All 10 already have a
project, so the drop-off is "what do I deploy", not "how do I sign up".

Offer decided by the owner: Startup plan free for 30 days, redeemed from a
tracked promo link. A/B variants are stored but a single variant ships first —
at n=10 a split measures nothing; cohorts are cut by registration date instead.

## Plan

- [x] 1. Migration `103_growth_campaign_sends.sql`: one row per (campaign,
      user), carrying the opaque promo token and the four funnel timestamps
      (sent / clicked / redeemed / converted).
- [x] 2. `notify.ComposeReactivation`: the customer-facing letter.
- [x] 3. `SweepReactivation`: picks the cohort, sends once, never twice, and
      backfills `converted_at` from the user's first successful build.
- [x] 4. Click tracking: `POST /api/v1/promo/click`, public and idempotent.
      Replaces the planned `GET /r/:token` redirect — on `console.dada-tuda.ru`
      the ingress routes only /api, /auth, /health, /mcp and a few fixed paths
      to the backend, so a vanity path would never have reached it.
- [x] 5. `POST /api/v1/promo/redeem` (authenticated): grants Startup for 30 days
      to the caller's own org and marks the send redeemed.
- [x] 6. `GET /api/v1/admin/growth/campaigns`: sent/clicked/redeemed/converted
      per campaign, variant and signup week.
- [x] 7. Frontend `/promo/[token]`: records the click, signs the recipient in
      with the return path preserved, redeems, then lands on the templates.
- [x] 8. Wire the sweeper in `cmd/server/main.go`, behind the billing sweeper
      tick and behind `REACTIVATION_CAMPAIGN_ENABLED` (default off).
- [x] 9. Verify: real-DB tests for the sweeper and the redeem gate, go vet,
      frontend build.

## Review

Shipped dark on purpose. `REACTIVATION_CAMPAIGN_ENABLED` defaults to false, so
deploying this code mails nobody: the first tick after the flag flips writes to
ten real mailboxes, and that is a decision to take deliberately, not a side
effect of a push.

What the tests actually prove (real Postgres, all six green):

- a dormant customer is enrolled and mailed once; an account that already
  shipped a build is not enrolled at all;
- a second sweep sends nothing more — the unique index, not the mail call, is
  the dedup guard;
- `converted_at` is stamped by a successful build after the letter, and a
  failed build does not count;
- the grant carries a 30-day term, never a perpetual paid plan;
- a forwarded link is refused with 403 and consumes nothing;
- a paying customer keeps their plan and their longer term (`granted: false`);
- the click endpoint answers 204 identically for live and unknown tokens, so it
  cannot be used to probe whether an address was mailed.

Test isolation note: these run against the shared cloud-console database, so
they sweep under a per-test campaign name. Running them under the live campaign
name would stamp `sent_at` on real dormant accounts that never got a letter and
the unique index would then refuse to ever mail them — the suite would burn the
campaign it exists to protect.

To go live: set `REACTIVATION_CAMPAIGN_ENABLED=true` on the console backend.
Funnel reads from `GET /api/v1/admin/growth/campaigns`.

## Review — Let's Encrypt на песочничной VM (2026-08-07)

- [x] VM `le-probe` в `agent-sandbox`, окружение `le-probe` (runtime=vm), приложение `le-web` (nginx:alpine)
- [x] Найден и починен продуктовый дефект: ингресс без сертификатов рендерился как 443-vhost с пустым `ssl_certificate` → CrashLoop nginx → падала маршрутизация всей машины (`dffb107f`)
- [x] Стирается протухший `error_message` при переходе AppServer в WaitingForAgent/Ready (`fc586380`)
- [x] Полный сценарий: ингресс `le-ing2` (канонический хост без TLS, отдаёт 200 по HTTP) → attach `le-probe.pv.dada-tuda.ru` → certbot-компаньон → `active/active`
- [x] Доказательство: `https://le-probe.pv.dada-tuda.ru/` → 200, issuer `C=US; O=Let's Encrypt; CN=YE1`, срок до 2026-11-05
- [x] DELETE AppServer на новом образе: VM снесена, `app_servers` пуст, IP отдан провайдеру
- [x] Уборка: приложения удалены, A-запись `le-probe` снята, TXT зоны превью не тронут (это платформенная запись `pv.dada-tuda.ru`)
- [ ] Осталось: строка окружения `le-probe` живёт (API удаления окружения нет) — стоимости не несёт

## Review — VM-трек готовых решений (2026-08-08)

Шаг 3 из плана «догнать каталог Бегета». Шаг 1 (image-трек) и шаг 2 (26 карточек
с категориями + UI) уже выкачены; здесь — те же карточки на `app_servers`.

- [x] `createAppOp`: том на compose-окружении больше не отбивается
      `storage_not_supported` — Longhorn-поля (size/storageClass/fsGroup) снимаются,
      остаётся путь монтирования. До этого ЛЮБОЕ stateful готовое решение было
      недеплоибельно на VM.
- [x] gitops-agent: `composeDesiredFromCreate` + `ComposeDataVolumeName` — том из
      payload превращается в docker named volume `<app>-data`, рендерер пиннит его
      `external: true`, деплой-воркер создаёт его перед стартом стека.
- [x] `Solution.Runtimes` + `SupportedRuntimes()`: по умолчанию оба субстрата
      (собранный из исходников апп тоже доезжает до VM — build-agent ставит
      CreateApp, а маршрутизирует уже окружение). Сужение — только для того, что
      субстрат физически не тянет.
- [x] Категория «Игровые серверы» и первая карточка: Minecraft Java
      (`itzg/minecraft-server:java21`, порт 25565, том `/data`, параметры EULA /
      версия / сборка / MOTD), `Runtimes: [vm]`.
- [x] Гейт в `InstallSolution`: несовместимая пара «карточка × runtime» — 400 с
      объяснением, какой субстрат подходит, до постановки любой операции.
- [x] Консоль: `runtimes` в типе `Solution`, проп `envRuntime`, карточки
      фильтруются под окружение; на VM-окружении с привязанным AppServer каталог
      теперь показывается (раньше пустой экран VM его прятал совсем).
- [x] Тесты: worker (`compose_create_volume_test.go`), каталог (инварианты
      runtime, VM-only для игр), API на реальной базе — установка Minecraft на
      k8s даёт 400 и ноль операций, на VM — CreateApp с `/data` и снятым size,
      EULA лежит в env_vars.

Проверено: `go test ./internal/...` (backend, TEST_DATABASE_URL на локальном
кластере) и `go test ./internal/worker` (gitops-agent) зелёные, `tsc --noEmit`
чистый.

Не сделано осознанно:
- [ ] UDP-игры (CS2, Rust, TeamSpeak, Factorio) и Terraria — апп объявляет один
      TCP-порт и не умеет аргументы команды. Нужен multi-port/protocol спек аппа,
      иначе карточка деплоится зелёной и не принимает игрока.
- [ ] FreePBX — по той же причине (SIP UDP + диапазон RTP).
- [ ] Ни одна из карточек ещё не установлена в `agent-sandbox` вживую.


## Переезд платформенных БД на shard-0 (шаг 6 postgres-multitenancy-design) — 2026-08-10

Итог: shard-1 держит только `odds-research`. Тикет fonbet про выделенный
инстанс закрыт нулевым новым диском — уехали остальные, а не клиент.

- [x] Все переезжаемые базы на shard-0 (`cloud-console`, `keycloak`, `fanbot`,
      `recog`, `test-db-1`, `codexlb`, `console-test` и прочие).
- [x] Сверка по таблицам через `md5(query_to_xml(...))`, а не по размеру.
      Единственное расхождение (`box_usage`) — артефакт `order by 1` по
      неуникальной колонке; подтверждено `string_agg` с детерминированным
      порядком.
- [x] Старые копии переименованы в `<db>--moved-to-shard-0`, не удалены.
      Забытый DSN обязан падать громко. Коннектов кроме `postgres` нет ни в
      одной.
- [x] Девять мусорных баз на shard-0 переименованы в `<db>--junk-2026-08-10`.
- [x] `codex-lb` перенаправлен с прямого адреса шарда на `pg-router`
      (argo-infra `71a65d04`).
- [x] Размещение записано в `db_moves`, CR клиентских баз несут
      `shard: shard-0`.
- [x] Баг рендерера: патч шарда бил в ПЕРВЫЙ манифест своего kind, а
      carrier-апп держит в одном файле все базы проекта → `ManifestOfKindNamed`
      (`200011fc`), тест на carrier-фикстуре, подтверждено вживую.
- [x] Баг воркера: `ClaimPending` не забирал операцию у умершего пода → окно
      перехвата 30 минут (`7e0e56d3`), тест на реальной базе + проверка
      ложной зелени (сломал WHERE — тест покраснел).

Не сделано осознанно:
- [x] Девять `*--junk-2026-08-10` снесены 08-11 вместе с семью осиротевшими
      ролями и десятью строками `db_moves`; `routes.ini` 37 → 30 строк.
- [ ] Снос `*--moved-to-shard-0` — после выдержки. Откатные
      `*--retired-2026-08-10` держим по `tasks/plan-retire-myuser.md`.
- [ ] «Выделенный» у `odds-research` — факт размещения, а не гарантия в CR.
      Гарантия требует диска, которого в кластере нет.
- [ ] Ответ клиенту (`tasks/reply-fonbet-postgres.md`) не отправлен.

## 2026-08-11 — box cold start: 16.7s -> убрать Longhorn из горячего пути

- [ ] `WorkspaceStore` (S3) + `NewWorkspaceStore` в пакете box
- [ ] workspace = emptyDir(sizeLimit=DiskGB) когда стор включён; PVC остаётся для легаси
- [ ] Suspend архивирует workspace в S3 ДО удаления пода; Resume восстанавливает после ready
- [ ] Destroy убирает объект
- [ ] config `BOX_WORKSPACE_S3_*` (дефолт = SOURCE_UPLOAD_S3_*), префикс `box-workspaces`
- [ ] argo-infra: quota ephemeral-storage + LimitRange max, StorageClass WFFC + strict-local
- [ ] тесты: canon-под не поехал, ephemeral-под, suspend/resume round-trip
- [ ] замер холодного бута в agent-sandbox + suspend/resume с файлом
