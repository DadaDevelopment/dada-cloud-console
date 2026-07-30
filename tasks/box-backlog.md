# Dada Box — бэклог пивота

Цель: эфемерный бокс с рутом за секунды, к нему подключается агент клиента, БД и S3 доцепляются
на ходу, выживший прототип кристаллизуется в постоянную VM с доменом. Одно окружение от мысли до
прода.

Планы (читать перед началом соответствующего блока):
`docs/plans/2026-07-29-box-runtime-architecture.md` ·
`docs/plans/2026-07-29-box-backend-slice.md` ·
`docs/plans/2026-07-29-box-crystallization.md` ·
`docs/plans/2026-07-29-box-test-and-measurement.md` ·
`docs/plans/2026-07-29-agentenv-open-profile.md`
Продукт: `docs/product/box-product-brief.md`

---

## БЛОКЕРЫ — решить до первой строки кода

- [ ] **B1. Бокс = строка `environments` (`runtime='box'`) или свой `ResourceKind`?**
      Два плана противоречат друг другу. Рекомендация — `runtime='box'` с мутацией на месте при
      кристаллизации, потому что `environment_id` это носитель удостоверения, к которому привязаны
      `env_vars`, `resource_snapshots`, `domain_hostnames`, и переиспользование строки **и есть**
      механизм продукта. Цена — аудит `api/runtime_guard.go`. **Решение основателя/архитектора.**
      Все задачи бэкенда ниже написаны под рекомендацию.
- [ ] **B2. Развилка «анти-lock-in против OEM-канала через хостеров».** Решение письменно.
      Если OEM — блок `agentenv` не начинать вообще и удалить из бэклога.
- [ ] **B3. `ls /dev/kvm` на Beget-инстансе** (30 минут). Закрывает вопрос Firecracker фактом.
      Если KVM есть — вернуться к рантайм-плану до слайса 1.
- [ ] **B4. Разговор с Beget и чтение ToS:** допустим ли парк VM, на котором исполняется чужой
      код. Бесплатно, может утопить продукт. Если нет — вопрос своего железа возвращается сразу.

---

## ФАЗА 0 — спайки (1 неделя, ничего не фиксируем без чисел)

- [ ] S1 gVisor `runsc` (systrap) внутри вложенной Beget VM: `npm install`, `cargo build`,
      `go build` под runsc против runc на том же боксе. **Провал: >2× на `npm install` → подложка
      выбывает целиком.** Делать первым.
- [ ] S2 `/dev/kvm` (см. B3)
- [ ] S3 rootless BuildKit + fuse-overlayfs в песочнице: собрать настоящий Dockerfile.
      Провал → обещание «docker внутри» переписывается **до** публикации лендинга
- [ ] S4 бюджет времени: захардкоженный скрипт, заранее поднятая песочница, захват, замер всех
      семи фаз. **До** написания control plane. Провал: p95 > 5 с → переделывать путь захвата
- [ ] S5 задержка «мозг локально»: одна настоящая задача Claude Code против бокса по MCP против
      локального прогона. Опубликовать число внутри
- [ ] S6 `runsc checkpoint`/restore: надёжность освобождения памяти при сне. Фолбэк на заморозке
      уже спроектирован, цена провала — только плотность

---

## ФАЗА 1 — измеримость (2 инж.-недели, идёт ПАРАЛЛЕЛЬНО фазе 0)

Делается до того, как рантайм существует: тогда остальные эмитят в определённый контракт.

### Предусловие CI (subagent: executor) — ГОТОВО

- [x] `postgres:16-alpine` в pod template `Jenkinsfile` (trust-auth, дефолтный каталог данных —
      **не** `/dev/shm`, там 64 МиБ по умолчанию и свежий кластер с WAL его переполняет)
- [x] `backend/cmd/migrate` — применяет миграции и **сам ретраит подключение**, поэтому в CI не
      нужен ни `psql`, ни угаданный `sleep`: Jenkins не ждёт готовности сайдкара перед шагами
- [x] в стадии `Backend tests` прогон миграций и `TEST_DATABASE_URL`
- [x] `TestCIRequiresDatabase` — **падает** при `CI=true` и пустом `TEST_DATABASE_URL`, локально
      скипается (проверено в обе стороны)
- [x] проверено на настоящем postgres 16: 62 миграции применяются,
      **66 тестов в `internal/api`, которые молча скипались, теперь исполняются и проходят**
      (212 PASS с базой против 146 без)
- [x] `make test-box`, `make migrate`
- [x] стадия фронтенда больше не съедает провал линта и типов. Форма
      `probe && npm run lint || echo "No lint script — skip"` привязывала `||` ко всей цепочке,
      поэтому настоящая ошибка линта давала exit 0 с обманчивым сообщением про отсутствующий
      скрипт. Заменено на `if/else`. **Доказано воспроизведением:** с внесённой настоящей ошибкой
      старая форма даёт 0, новая — 1. Это и была системная причина, по которой ошибка
      `react-hooks/set-state-in-effect` в `box-demo.tsx` дожила до ветки
- [ ] **найдено попутно, не блокирует:** у `gitops-agent` есть тесты с БД, которые в CI
      скипаются — стадия `GitOps-agent tests` не получает `TEST_DATABASE_URL`, его выставляет
      только `Backend tests`. На чистой базе там **6–7 падений, и счёт плавает** между прогонами,
      то есть тесты зависят от порядка и делят состояние. Проверено: на `origin/main` — 7,
      на этой ветке — 6, и ни один файл `gitops-agent` здесь не изменён. Когда кто-нибудь даст
      этой стадии базу, сборка станет красной. Починка — отдельная задача в своём модуле:
      изолировать состояние между тестами, потом дать стадии базу и держать её зелёной


### Контракт метрик (subagent: executor) — ГОТОВО

- [x] `backend/internal/metrics/box.go` — 27 метрик, `BoxReadyBudget = 10s`, бакеты
      `0.5…120` для жизненного цикла и `5…600` для кристаллизации, `RecordBoxReady` с
      **атрибуцией нарушения бюджета доминирующей фазе** (детерминированный тайбрейк, чтобы
      порядок обхода map не заставлял алерт мигать)
- [x] `backend/internal/metrics/box_surface.go` — объявленная поверхность таблицей
- [x] `TestBoxMetricSurfaceGolden` + `backend/tests/golden/box/metrics.txt`
- [x] `TestBoxMetricSpecsMatchCollectors` — таблица сверяется с тем, что реально
      зарегистрировано. **Двухсторонняя защита:** golden ловит расхождение записи с намерением,
      этот тест ловит расхождение записи с реальностью. Без второго golden пинил бы ложь
- [x] `TestBoxMetricSurfaceConventions` — суффиксы по типу, запрет лейблов неограниченной
      кардинальности (`org_id`, `box_id`, …)
- [x] `TestAlertedMetricsAreDeclared` — статический скан репозитория. **Статический, а не по
      реестру:** у неиспользованного `CounterVec` нет детей, поэтому проверка по реестру прошла бы
      при опечатке в имени. Покрывает и существующие алерты
- [x] группа `dada-cloud-console.box`, 6 алертов, `BoxCrystallizeStateLoss` — единственный critical
- [x] `values.yaml`: `boxReadyBudgetSeconds`, `boxReadyP95Seconds`, `boxMeterStaleSeconds`
- [x] `docs/runbooks/box-latency-budget.md`
- [ ] **осталось:** подключить gauge к циклу коллектора (`dada_boxes{phase}`,
      `dada_box_failed_recent`, `dada_box_pool_*`, `dada_box_spend_cap_max_ratio`,
      `dada_box_crystallizations_pending_age_seconds`). Метрики объявлены и запинены, но обновлять
      их нечем: запросы нужны к таблице `boxes`, которой ещё нет. Делать в фазе 2 вместе с `058`

### Швы и герметичные тесты (subagent: executor) — ГОТОВО

- [x] `backend/internal/box/box.go` — `BoxRuntime`, `WarmPool`, `AttachProvider`, `Crystallizer`,
      `CarryManifest` с `preserved|recreated|lost`
- [x] `phases.go` — `PhaseTimeline`. **У него нет метода, принимающего метку времени от
      вызывающего** — это и есть весь дизайн: единственный способ закрыть фазу это спросить часы
      оркестратора в момент события, поэтому время от гостя не может попасть в замер даже случайно
- [x] `readiness.go` — канарейка отдаёт `key=value`, а не сырые баннеры версий, поэтому разбор
      точный, а не набор regex по вендорским форматам
- [x] `pool.go` — `MemoryPool`, референсное поведение: захват ровно однократный, исчерпание отдаёт
      `ErrPoolExhausted` вместо блокировки
- [x] `spawn.go` — путь готовности с записью шагов
- [x] `fake.go` — `FakeClock`, `FakeRuntime`, `NewWarmFixture`. Задержки применяются продвижением
      часов, а не `sleep`, поэтому тест про 40-секундную загрузку исполняется мгновенно и
      детерминированно
- [x] `readypath_golden_test.go` + `backend/tests/golden/box/ready-path.txt` — 8 шагов
- [x] `TestSpawnIgnoresGuestReportedTime` — гость заявляет время на 26 лет мимо, замер не сдвигается
- [x] `TestSpawnRefusesToPublishAnInconsistentMeasurement` — часы назад → spawn падает, а не
      публикует бессмысленную длительность
- [x] `TestReadinessRequiresTheWarmToolchain`, `TestReadinessRejectsAcceptButUnusable`,
      `TestCanaryCommandProbesEveryRequiredTool` (команда и список сверяются друг с другом:
      расхождение в любую сторону молчаливо)
- [x] `TestPoolClaimIsExactlyOnceUnderConcurrency` — 100 горутин на 10 боксов
- [x] `TestSpawnClassifiesRejections` — quota / spend_cap / pool_exhausted / холодный образ
- [x] `TestSpawnOrchestrationOverheadIsNegligible`
- [x] всё зелёное: `go build ./...`, `go vet ./...`, `gofmt`, 26 пакетов `go test`

### Воронка (subagent: executor, не зависит ни от чего)

- [ ] cookie `dada_vid` (опаковый UUID, 400 дней, `SameSite=Lax`, `Secure`) по правилам
      `docs/architecture/yandex-metrika-uid-cookie.md`. **Никогда email и никакие ПД (152-ФЗ).**
      `dada_uid` не переиспользовать — он только для аутентифицированных
- [ ] `page_view` в `KNOWN_EVENTS` в `frontend/app/api/box/lead/route.ts`, раз на сессию,
      дедуп по `dada_vid`
- [ ] `backend/migrations/057_box_leads.sql`: `box_leads`, `box_funnel_events` (по образцу
      `040_feedback.sql`) + `GRANT ... TO dada`
- [ ] аннотированный `POST /api/v1/box/leads`; Next-роут форвардит и **сохраняет** лог и вебхук как
      fail-open фолбэк
- [ ] `utm_source=door_box` на всех CTA лендинга (для сравнения с существующими `door_*`)
- [ ] `box_grants(claim, org_id, box_id, granted_at, granted_by)` + админский эндпоинт.
      **Обязательно:** без него у заголовочной метрики нет источника данных
- [ ] view `box_repeat_use_7d` по определению: сессия — непрерывный отрезок активных минут, разрыв
      >30 мин начинает новую; повторное использование — ≥2 сессий, старты разнесены на ≥24 ч, обе в
      7 днях от первой активации
- [ ] `docs/runbooks/box-funnel-metrics.md` с точным SQL всех четырёх метрик
- [ ] **Не наряжать прокси в измерение второго использования.** Пока таблиц нет, честный источник —
      тред оператора, помеченный как таблица с именами

---

## ФАЗА 2 — бэкенд, слайс 1: объект и жизненный цикл (3–4 д)

### Миграции (subagent: executor)

- [ ] `058_boxes.sql` — `environments_runtime_check` расширить до `('k8s','vm','box')` через
      `DO $$ ... EXCEPTION WHEN insufficient_privilege` с проверкой, что `box` уже валидируется;
      таблица `boxes` (`environment_id UNIQUE`, статусы, TTL, `spend_cap_rub`,
      `last_sample_json`); `idx_boxes_project_name_live` частичный `WHERE status <> 'Deleted'`
- [ ] `062_grant_box_tables.sql` — явный `GRANT` на все таблицы бокса роли `dada`

### Модель и каталог (subagent: executor)

- [ ] `backend/internal/models/box.go` — `Box`, `BoxStatus`, 10 констант действий, 10
      payload-структур. Комментарий «JSON-теги — жёсткий контракт с воркером, НЕ переименовывать»
- [ ] `backend/internal/boxcatalog/catalog.go` — образы и профили по образцу
      `profiles/catalog.go` (замороженная переменная, `Lookup`, `Names`). **Не таблица**

### API (subagent: executor)

- [ ] `backend/internal/api/boxes.go` — list/create/get/state/delete/suspend/resume/extend
- [ ] `backend/internal/api/webhooks_boxagent.go` — статус и сэмплы. **Регистрировать внутри гарда
      `if verifier ok` и НЕ аннотировать** (иначе протекут в coverage-гейт и в MCP-поверхность)
- [ ] роуты в `router.go`; полные аннотации swaggo на все аннотируемые; регенерация
      `swag init` и **коммит всех трёх** файлов (`swagger.json`, `swagger.yaml`, `docs.go`)

### МИНА (subagent: executor) — в этом же PR, не в следующем

- [ ] `gitops-agent/internal/db/operations.go`: добавить все 10 имён действий в денилист
      `NOT IN (...)`. Без этого gitops-agent захватит `BoxUp` и провалит с `unknown action`
- [ ] `go build ./...` во всех четырёх модулях (`backend`, `gitops-agent`, `portainer-agent`,
      `mcp-server`)

### Следствие B1 (subagent: executor) — блокирующее

- [ ] аудит **каждого** ветвления по `environments.runtime`: `api/runtime_guard.go`,
      `databases.go`, `billing_consumption.go`, рендереры. Составить список и обработать `'box'`
      явно в каждом. Не «по ходу», а отдельной задачей с чеклистом

### Документы

- [ ] `docs/adr/ADR-016-box-runtime-gvisor-on-beget-vms.md`
- [ ] `docs/adr/ADR-017-box-hosts-outside-the-portainer-fleet.md` — отклонение от ADR-007:
      `bootstrap.sh.tmpl` монтирует `docker.sock` и `/:/host`, на хосте с враждебным кодом это
      root-эквивалент к каналу управления всем парком

### Verify

- [ ] `POST /boxes` → 202 + строка `boxes` + строка `environments` с `runtime='box'`
- [ ] curl'нутый вебхук переводит в Ready с координатами SSH; `GET .../state` их показывает
- [ ] `go test ./...` зелёный во всех модулях; `openapi_coverage_test` зелёный

---

## ФАЗА 3 — бэкенд, слайс 2: креды и одна дверь (2 д)

- [ ] `059_box_attachments_sessions.sql` (часть `box_sessions`) — копия формы `app_deploy_hooks`:
      только sha256 и `token_prefix`, plaintext один раз, `revoked_at` вместо `DELETE`
- [ ] `box_sessions.go` — `generateBoxToken()` → `dadabox_` + 40 hex; `boxSessionFromToken(c)`;
      TTL по умолчанию 12 ч, максимум 168 ч; sweep истёкших раз в минуту
- [ ] **`DeleteBox` и `SuspendBox` ставят `revoked_at=now()` на все живые сессии бокса ДО
      постановки в очередь** — живой SSH-кред не должен переживать enqueue
- [ ] `boxes_up.go` — `boxUp` (ограниченный long poll, `wait_seconds` в [0,120]), `listAllBoxes`,
      `getBoxConnection`, `getBoxCatalog`. Ответ несёт `connect` с готовым сниппетом `mcpServers`
- [ ] `default_overrides.yaml` — 14 записей `keep`. **Регенерировать спеку ДО правки keep-листа**,
      иначе `curation_test.go` падает с невнятным «some keep names don't match»
- [ ] **Инструмента `boxExec`/`boxRunCommand` не добавлять.** Мозг клиента остаётся локально, бокс
      отдаёт свой MCP-эндпоинт
- [ ] `mcp-plugin/README.md` и `.claude-plugin/marketplace.json` — счётчик инструментов, теги
      `box`, `sandbox`, пример «поднять бокс»

### Verify

- [ ] из живой сессии Claude Code **один вызов MCP** возвращает команду SSH, MCP URL, одноразовый
      токен и цену за минуту
- [ ] `curation_test.go` и `boot_smoke_test.go` зелёные

---

## ФАЗА 4 — рантайм: один хост, настоящая изоляция (4 нед)

Новый модуль `box-agent/` со своим `go.mod` (конвенция «модуль на сервис»).

- [ ] `box-agent/cmd/box-agent` — демон хоста; `cmd/boxd` — супервизор в песочнице
- [ ] `internal/sandbox/{runsc,spec,overlay}.go` — OCI-спека и runsc. **Шов, который позже
      реализует бэкенд Firecracker**
- [ ] `internal/netns/{netns,nftables,shaper}.go`, `internal/cgroup/limits.go`,
      `internal/quota/xfs.go`, `internal/image/prewarm.go`, `internal/tunnel/client.go`
- [ ] `backend/cmd/box-broker` + `backend/internal/boxbroker/*` по конвенции gateway; helm-шаблоны
      `box-broker-{deployment,service,ingress,secret}.yaml`
- [ ] **Захват бокса синхронен и НЕ идёт через `operations`** — опрос воркера 5 с
      (`VM_POLL_INTERVAL_DB`) больше всего бюджета
- [ ] тёплый образ v1, пин по digest: ubuntu 24.04, node LTS, python 3.12+uv, go, rust, сборочные
      тулы, `psql`/`redis-cli`/`mc`/`aws`, tmux, sshd, `boxd`, **предпрогретые кеши пакетов**
- [ ] фиксированный пул 8 тёплых боксов вручную

### Изоляция — гейт публичного доступа, все 10 обязательны

- [ ] каждый процесс тенанта под `runsc`, никогда `runc`
- [ ] на хостах боксов только боксы: без `docker.sock`, без агента Portainer, не в `dada-vms`
- [ ] user namespace + ремап uid, без `CAP_SYS_ADMIN`/`SYS_MODULE`/`NET_ADMIN`, `no_new_privs`
- [ ] netns+veth на бокс, **нет пересылки бокс↔бокс**, egress default-deny, RFC1918/link-local/
      `169.254.169.254`/CIDR платформы в чёрной дыре
- [ ] overlay upper на XFS project quota, никогда общего writable-монтирования
- [ ] cgroup v2 `cpu.max`/`memory.max`/`memory.high`/`memory.swap.max=0`/`pids.max`/`io.max`.
      **Никакой переподписки памяти**
- [ ] шейпинг egress (tc/HTB) + байтовая квота по счётчикам nftables
- [ ] хост: без входящих кроме SSH с наших /32, автообновления безопасности, auditd
- [ ] подтверждённый email + привязанный платёж до первого бокса; лимит параллельности по репутации
- [ ] `expose` только через ingress брокера на `*.box.dada-tuda.ru`; IP хостов не публикуются

### Verify

- [ ] альфа для лидов с fake-door, выдача с участием оператора
- [ ] `scripts/box-rehearse.sh` (30 spawn'ов): p50 ≤ 3,0 с, max ≤ 15 с, промахи пула ≤ 2%

---

## ФАЗА 5 — пул, абьюз, публичный гейт (3 нед)

- [ ] контроллер пула в `portainer-agent` (`internal/boxhost/`, `worker/create_boxhost.go`),
      переиспользуя `internal/terraform` + `internal/beget` + `internal/ssh` дословно
- [ ] размер пула `max(4, ceil(1.5 × p95 часовых захватов))`; наращивание при нехватке 60 с,
      сокращение с кулдауном 20 мин, слив только пустых хостов
- [ ] SMTP 25/465/587 закрыт; порты майнинг-пулов в set nftables из фида; лимиты packet-rate и SYN
- [ ] **эвристика майнинга без DPI:** ~100% на всех ядрах >30 мин при почти нулевом дисковом IO и
      крошечной сети → зажим до 25% CPU и флаг
- [ ] **никаких своих доменов на эфемерных боксах** (это фича кристаллизации; заодно убирает
      большую часть стимула к фишингу); `X-Robots-Tag: noindex`, kill switch, проверка репутации
      URL при первом expose
- [ ] уровень 0 для новых: 1 бокс, TTL 2 ч, 5 ГиБ egress первые 24 ч; отпечаток платёжного
      инструмента через `payment_connections`
- [ ] **egress при превышении троттлить, а не убивать** — убийство теряет работу клиента
- [ ] `scripts/box-load.sh` (50 параллельных spawn'ов через `xargs -P`), `box-runaway.sh`,
      `box-abuse.sh`. **Проверка шумного соседа:** время готовности соседнего бокса внутри бюджета
      всё время сжигания
- [ ] `Jenkinsfile.nightly` (отдельный файл, не ветка деплой-пайплайна), cron `H 2 * * *`

### Verify

- [ ] все 10 контролей изоляции зелёные — **это и есть гейт, а не дата**
- [ ] логируются только метаданные: байты, направления, запрещённые вызовы. **Никогда содержимое**

---

## ФАЗА 6 — attach и expose (2 нед)

- [ ] `059_box_attachments_sessions.sql` (часть `box_attachments`) + `box_attachments.go`
- [ ] **Проверить D2 на реальном создании:** `runtime='box'` попадает в не-`vm` ветку
      `CreateServiceDatabase` → управляемый Crossplane Postgres вне бокса. Если держится, менять
      `databases.go` не нужно
- [ ] `AttachBoxDatabase` ставит **дочернюю** `CreateServiceDatabase`, ждёт `Committed`, читает
      кред `DBCredentialsResolver`, внедряет env
- [ ] форвардер брокера: слушатель `127.0.0.1:5432` **внутри netns бокса** через существующий
      туннель хоста. **Не выставлять Postgres тенанта публично** ради временного тела
- [ ] S3: существующая `CreateS3Bucket`, `cloudtask/s3creds.go`, внедрить `AWS_*` и `S3_ENDPOINT`,
      добавить endpoint в allowlist egress
- [ ] `box_expose.go` — суррогатные домены миграции 030 + pdns + `AttachDefaultDomain`
- [ ] **wildcard `*.box.dada-tuda.ru` по DNS-01, реплицированный на хосты.** Per-host LE на бокс
      упирается в 50 сертификатов на домен в неделю — **50 боксов в неделю заканчивают продукт**

---

## ФАЗА 7 — метеринг, потолки, reaper (4 д)

- [ ] `060_box_usage.sql` — PK `(box_id, minute_start, kind)`.
      **Простаивающая минута не создаёт строки вообще**
- [ ] `billing/data/box-fleet-cost.yaml` + `LoadBoxFleetCost` (отдельное железо; складывать в
      `cluster-cost.yaml` нельзя — молча размоет стоимость vCPU для k8s)
- [ ] `costengine.MinutesPerMonth = 43200` + `PerMinuteCost`. На 43 200 минутах цена равна месячной
      цене VPS — тезис «матчится с VPS» держится арифметикой
- [ ] `box_meter.go` — тик 60 с, **без advisory-лока** (PK делает идемпотентным).
      Активность: сессия открыта, либо CPU > `BOX_ACTIVE_CPU_PERCENT` (5%), либо HTTP-запрос, либо
      операция в полёте
- [ ] **Авторитетный сигнал — сэмпл box-agent снаружи гостя.** Heartbeat внутри гостя может
      попросить только *больше* биллинга, никогда меньше
- [ ] `box_reaper.go` + `lockKeyBoxReaper` — **под advisory-локом** (ставит операции, шлёт почту).
      Сон 15 мин простоя, TTL 8 ч, утилизация 72 ч сна после двух писем.
      **Письмо об утилизации — момент апсейла в кристаллизацию**
- [ ] потолок: `SUM(cost_rub)` ≥ `spend_cap_rub` → **suspend, никогда delete**
- [ ] три правки в существующий биллинг: `box_minutes` в `countable`; `box_minutes` в квоты
      `plans.yaml`; `consumptionBoxes` + **`AND e.runtime <> 'box'`** в `consumptionApps`
- [ ] `scripts/box-idle-billing-rehearsal.sh`, `scripts/box-unit-cost-check.sh` (сверка с OpenCost,
      маржа на бокс-час, алерт при отрицательной)

### Verify

- [ ] простаивающий бокс накапливает **0**; занятый виден в `/billing/consumption` рядом с
      приложениями; потолок → авто-suspend и письмо, данные целы

---

## ФАЗА 8 — кристаллизация (12 нед, ОДИН инженер, НЕ начинать до сигнала)

**Предусловие:** не начинать, пока метрика второго использования не прошла. Бриф прямо говорит:
если `crystallize_intent` близок к нулю — закрывать, а не докручивать.

Порядок сознательно противоинтуитивен: **верификация выходит раньше переноса данных.**

- [ ] 8.1 скелет: `061_box_crystallizations.sql`, `operations.parent_operation_id`, частичный
      уникальный индекс на одну кристаллизацию на бокс, `box_plan.go` (чистый планировщик),
      пара API план+запуск, машина состояний. **Кристаллизовать пустой бокс** (1,5 нед)
- [ ] 8.2 захват образа: `docker commit` → Nexus → pull, подъём цели на существующем
      `CreateAppServer`. Тег `boxes/<box-id>:<op-id>` — op-id **и есть** ключ идемпотентности (1,5 нед)
- [ ] 8.3 **отчёт верификации и `RollingBack`** — манифест `(path,size,mode,sha256)` с обеих
      сторон, не счётчик байт и не «tar вернул 0»; список исключений печатается частью отчёта;
      env сравнивается по sha256 на ключ, никогда значения; **список несовпавших процессов
      рендерится явно**; сквозная проверка снаружи платформы; `SELECT 1` и `HEAD` изнутри цели (1,5 нед)
- [ ] 8.4 синхронизация томов, тёплая + дельта. **Postgres внутри бокса обязан быть остановлен
      перед финальным проходом** — файловая копия живого PGDATA битая; preflight обнаруживает и
      парковка в `WaitingForApproval` с раскрытым окном (2 нед)
- [ ] 8.5 **out-of-band `.env` по SSH** в `/etc/dada/stacks/<stack>/.env` 0600 + `env_file:` в
      compose. Исправляет существующее нарушение: `dbwatcher.go:1334` рендерит секреты в plaintext
      в git. **Самостоятельно ценно, можно подтянуть вперёд** (1 нед)
- [ ] 8.6 переход адреса: ранний DNS до переключения, ACME на standby-стеке (только nginx),
      **не больше 2 реальных попыток LE на имя на операцию, счётчик персистится на строке бокса**,
      staging LE для прогонов, backoff по `hostnameDNSStuckAfter=4m`/`hostnameReissueCooldown=15m`,
      **временное имя никогда не удаляется — становится 308**. Самая рискованная фаза, запас
      расписания держать здесь (2 нед)
- [ ] 8.7 захват процессов из `/proc/<pid>/{cmdline,cwd,environ}` + таблица сокетов, перезапуск под
      супервизором с health-гейтом. **Это НЕ убитый дизайн вывода спеки из pty** — записывается
      живая таблица процессов ядра, а файловая система уже у нас в закоммиченном образе (1 нед)
- [ ] 8.8 reaper хранения 72 ч, переход биллинга, UI саги. В консоли писать «бокс храним 72 часа,
      если чего-то не хватит», **а не «можно откатить»** (1,5 нед)
- [ ] `docs/adr/ADR-018-crystallization-contract.md` — что переезжает, что перезапускается,
      точка фиксации, обратимая и необратимая плоскости, и почему захват процессов не является
      убитым pty-дизайном
- [ ] `scripts/box-crystallize-rehearsal.sh` с негативным контролем по образцу `vm-rehearse.sh`

### Verify

- [ ] `crystallize-plan` отдаёт точный инспектируемый манифест с `carry_gaps` до любых действий
- [ ] `CrystallizeBox` терминален на **`Committed`**, не на `Ready` — записать в контракт, иначе
      ложный таймаут при опросе (баг уже зафиксирован в review-разделе `tasks/todo.md`)
- [ ] `dada_box_crystallize_state_loss_total` остаётся нулём на трёх ручных кристаллизациях
      реальных клиентов **до** автоматизации

---

## ФАЗА 9 — agentenv, открытый профиль (4 нед, только если B2 = анти-lock-in)

- [ ] `rationale.md` и список non-goals внутри, до публикации
- [ ] `cmd/agentenv` + `internal/envmodel` — 7 глаголов `env_*` (5 д)
- [ ] `internal/adapter/docker` — локальный Docker, он же референсная реализация (3 д)
- [ ] `internal/adapter/ssh` — любой VPS, паттерны из `portainer-agent/internal/ssh/` (3 д)
- [ ] `internal/mcpserver` — `agentenv mcp` как stdio MCP-сервер, 7 рукописных инструментов (2 д)
- [ ] `spec/DRAFT-wire-contract.md` — 6 страниц, **ненормативный**, с non-goals (2 д)
- [ ] `examples/` — манифест плагина Claude Code, конфиги Cursor и Codex (1,5 д)
- [ ] README en+ru, одна запись asciinema, CI с релизными бинарями (3 д)
- [ ] **Слово «протокол» не использовать.** «Профиль» и «спецификация»
- [ ] **Композиция с `devcontainer.json`, не замена**
- [ ] Apache-2.0 + `TRADEMARKS.md`. Слово «Box» в открытом репозитории не использовать
- [ ] нормативная спека, схемы и conformance — **v0.2, gated** на ≥10 пользователях CLI,
      запускавших его в две разные недели

### Критерии закрытия

- [ ] день 30: <10 пользователей в две разные недели → **блокирует v0.2**
- [ ] день 90: ноль внешних реализаций и ноль внешних PR с адаптерами → удалить `spec/` и
      `conformance/`, переименовать в «клиент и адаптеры», сказать об этом публично одним абзацем
- [ ] месяц 6: единственные реализации наши → понизить навсегда, перестать называть стандартом

---

## Контент под анонс

- [ ] `docs/product/launch/habr-article.md` — вычитать, заменить целевые числа на измеренные либо
      оставить явно как цели
- [ ] `docs/product/launch/social-posts.md` — LinkedIn и X, ru+en. Публиковать **после** Habr
- [ ] лендинг `/box` и `/en/box` — живой; блок «что уже работает, а что нет» держать актуальным
- [ ] баннер Box на главной — при появлении данных fake-door решить, повышать ли его в герои
- [ ] `frontend/lib/box-copy.ts` → перенести в `lib/i18n/dict.ts`, когда Box перестанет быть
      экспериментом

---

## Постоянные правила

- **Обещание не протекает.** Любое обещание на лендинге, которое архитектура не держит,
  переписывается **до** публикации, а не после. Одна протечка убивает доверие, а доверие здесь и
  есть товар.
- **Изоляция — себестоимость, а не фича.** Не продаётся, но без неё нельзя открываться.
- **Логируем метаданные, никогда содержимое.** Ни команд, ни нажатий, ни трафика тенанта.
- **Токены не перепродаём.** Клиент приводит своего агента.
- **Прокси не наряжаем в измерение.** Репозиторий заплатил за этот урок дважды.
