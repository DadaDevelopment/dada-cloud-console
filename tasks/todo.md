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

**Не сделано / дальше.**
- [ ] Проверить через сутки после выката: `box_usage` за день, доля
      `suspended_disk`, и что `dada-boxes` пуст, кроме живых боксов

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
