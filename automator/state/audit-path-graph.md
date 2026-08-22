# audit_events разбор — sess-0822h(2), 2026-08-22 12:3x UTC

Гейт: psql жив напрямую (`route -n get` дал `utun6`, но `ensure-proxy.sh`
сказал DIRECT-OK — прокси не понадобился в этот раз). DSN из
`argocd-prod/dada-cloud-console-backend#DATABASE_URL`, база `cloud-console`,
под `postgresql-0` в ns `databases`. `select now()` = 2026-08-22 12:32:29 UTC.

## 0. Окно с прошлого цикла (03:11:20 → 12:32, ~9ч): 114 новых строк, 81 реальных

`select count(*) from audit_events where created_at > '2026-08-22 03:11:20'`
= **114**. Разбивка по `actor_type`: `user` 81, `system` 33. Из 81
user-строк — 6 наш sandbox-тест (`alexkekiy@dada-tuda.ru` create/resize +
`eb82167d…@keycloak.local` delete `storage-resize-probe`, убран), **75 от
реальных внешних юзеров**: `artempro2022@yandex.ru` (активная сессия —
build/deploy/env/agent-chat цикл, см. §4), `artempro2021@bk.ru` (build fanvk,
`PlatformRecoveryPromptServed`), `michaelharlam@yandex.ru` (сессии без
глубокого взаимодействия), `artemmendeleev@gmail.com` (ViewApps only). Это
первое за 2 цикла окно с ощутимым реальным сигналом.

## 1. Новые юзеры: 48ч и 30д

48ч: **1** — `tarotreaderhimu@gmail.com` (2026-08-21 13:58 UTC), без изменений
с прошлого цикла (окно 48ч не захватило новых регистраций сверх неё).

30д: **30**, без изменений численно (6-й цикл подряд плато).

## 2. Zero-everywhere по 30д-когорте: 0 юзеров

[live] join `users`×(`audit_events` по `actor_id`, `builds` по `triggered_by`,
`git_repos` по `created_by`, `agent_chat_messages` по `user_sub`, `feedback`
по `user_sub`) по всем 30 юзерам — **0 с нулём везде**, у каждого минимум
1 audit-строка (обычно SignUp). Провалов инструментирования по этой оси нет.
Фермерская волна 08-08 (14 юзеров, audit_cnt=1, всё остальное 0) — известный
класс `project_signup_farm_wave_pollutes_funnel.md`, не новая находка.

## 3. Граф переходов (30д, `actor_type='user'`, исключены `@dada-tuda.ru`)

### Топ переходов A → B без self-loop (n=12)
| A | B | cnt | distinct users |
|---|---|---|---|
| SessionStart | ViewProject | 229 | 10 |
| ViewProject | ViewApps | 208 | 12 |
| ViewApps | ViewApp | 107 | 8 |
| ViewApps | SessionStart | 102 | 8 |
| ViewProject | SessionStart | 70 | 8 |
| ViewApps | ViewProject | 58 | 8 |
| ViewApp | ViewApps | 51 | 7 |
| DeployImageVersion | SessionStart | 40 | 4 |
| UploadSourceArchive | ViewBuildLogs | 36 | 4 |
| ViewProject | ViewApp | 34 | 5 |
| TriggerBuild | DeployImageVersion | 30 | 3 |
| SetEnvVar | DeployImageVersion | 29 | 4 |

Self-loops (не в таблице выше, но реальны): `ViewProject→ViewProject` 170/7,
`SeedDatabaseDSN→SeedDatabaseDSN` 169/2 (весь объём — retry-паттерн kkartov +
tarotreaderhimu, см. §5 прошлых циклов), `SetEnvVar→SetEnvVar` 73/7,
`SessionStart→SessionStart` 66/9 — навигационный шум/ретраи, не новый путь.

### ПЕРВОЕ действие после регистрации (rn=1 по created_at, action=SignUp
редко попадает в audit — большинство юзеров стартуют сразу с продуктового
действия)
| действие | кол-во юзеров |
|---|---|
| SessionStart | 19 |
| ConnectGitRepo | 8 |
| SignUp | 5 |
| CreateApp | 3 |
| CreateProject | 1 |
| CreateAppServer | 1 |
| AgentChatActionDeclined | 1 |
| RedeemPromo | 1 |

Если rn=1=SignUp, ВТОРОЕ действие (n=5 юзеров с явной SignUp-строкой):
SessionStart 12*, TriggerBuild 8, UploadSourceArchive 2, ConnectGitRepo 1,
CreateAppServer 1, ViewProject 1 (*числа считаются по всей 30д-когорте потому
что второе действие могло идти без явной SignUp-строки — таблица
ориентировочная, основной сигнал: после регистрации юзер либо сразу
подключает гит (8), либо стартует сессию и уже внутри неё коннектит репо/грузит
архив).

### ТЕРМИНАЛЬНОЕ действие — топ-5 по частоте среди тех, кто молчит >24ч
| действие | кол-во юзеров |
|---|---|
| SessionStart | 18 |
| TriggerBuild | 4 |
| CreateApp | 3 |
| ViewApps | 3 |
| DeployImageVersion | 1 |

**Важно про метод:** терминал считается по MAX(created_at) СТРОГО с фильтром
`actor_type='user'` — не по MAX(created_at) вообще. Разница материальна, см. §4.

## 4. НАХОДКА ЦИКЛА: system-actor audit-строки маскируют реальный churn под
чужим actor_id — искажение до 269 часов

[live] Раньше (все прошлые циклы, включая §8 прошлого файла) терминал считался
как "последняя audit-строка юзера" без разбора `actor_type`. Это ЛОЖНО,
потому что автоматический билд-пайплайн и self-heal-rebuild пишут
`BuildFinished`/`DeployImageVersion` с `actor_type='system'`, НО `actor_id`
= UUID реального юзера (не zero-UUID), когда сборка триггерится пушем/вебхуком
без живой сессии.

Замер: для каждого юзера сравнил `MAX(created_at) WHERE actor_type='user'`
против `MAX(created_at) WHERE actor_type='system' AND actor_id=его id`:

| юзер | последнее РЕАЛЬНОЕ действие | последняя system-строка под его id | искажение |
|---|---|---|---|
| **sergeykozlov2006@gmail.com** | 2026-08-09 14:25 (ViewApps) | 2026-08-20 19:37 (DeployImageVersion, magic-mirror) | **269.2ч** |
| lifecoachrussia@yandex.ru | 2026-08-19 09:41 (TriggerBuild) | 2026-08-20 07:16 (DeployImageVersion, self-heal) | 21.6ч |
| tarotreaderhimu / artempro2022 / eb82167d (sandbox) | — | — | ~0 (system-строка почти сразу следом, не искажает) |

**`sergeykozlov2006` — скрытый churn, невидимый 11 дней.** Его апп
`magic-mirror` автоматически пересобрался и передеплоился 3 раза 08-20
19:00–19:37 (все `success`, `actor_type='system'`, resource `magic-mirror`) —
это НЕ его клик, юзер молчит с 08-09. Ни один прошлый цикл его не видел как
churn-кандидата, потому что naive-запрос `MAX(created_at)` по его `actor_id`
указывал на 08-20, внутри 48ч-окна "жив". Реальная тишина сейчас — **317ч
(13.2 дня)**, это ЧЕТВЁРТЫЙ подтверждённый churn, пропущенный 6+ циклов
подряд из-за метода замера, а не из-за отсутствия сигнала в данных.

Источник в коде (READ-ONLY, не менял): `BuildFinished` audit-строка пишется
`build-agent/internal/db/builds.go:358-360` через
`COALESCE(triggered_by, created_by, <zero-uuid>)` — для пуш/вебхук-сборок
`triggered_by` в `builds` NULL, поэтому падает на `git_repos.created_by`
(владелец репо), а не на zero-UUID. `DeployImageVersion` — тот же паттерн в
`build-agent/internal/db/deploy.go:354-362` (`handoffActor()`, возвращает
`*repo.CreatedBy` когда `b.TriggeredBy == nil`). Для контраста —
`PlatformSelfHealRebuild` уже делает это правильно:
`backend/internal/api/platform_selfheal.go:196-212` →
`recordSystemAudit` → `backend/internal/api/audit.go:446-448` →
жёстко `systemDeployActorID` = `00000000-0000-0000-0000-000000000000`
(константа в `backend/internal/api/deploy_hooks.go:1`). То есть паттерн
"system-действие = zero-UUID" в кодовой базе уже есть и уже работает для
self-heal — просто `build-agent` для обычного BuildFinished/DeployImageVersion
его не унаследовал.

**Ревизия churn-состава этого цикла:**
| актор | терминал (по actor_type='user') | часов тишины | классификация |
|---|---|---|---|
| kkartov@yandex.ru | ViewApps | ~65.1 | churn (без изменений) |
| good.win2283@gmail.com | ViewApps | 191.3 | churn (без изменений) |
| cryocrm@gmail.com | ViewProject | 267.4 | churn (без изменений) |
| **sergeykozlov2006@gmail.com** | ViewApps | **317** (было невидимо) | **НОВЫЙ подтверждённый churn — на самом деле старый, дата регистрации 06-30, тишина с 08-09** |
| lifecoachrussia@yandex.ru | TriggerBuild | 74.9 (было "45.0, растёт к границе" — это тоже было занижением из-за той же ошибки метода) | **пересекла 48ч → ПЯТЫЙ подтверждённый churn**, не "растёт к границе" |

Итого подтверждённый churn 30д-когорты: было 3 (kkartov, good.win2283,
cryocrm), стало **5** (+ sergeykozlov2006, + lifecoachrussia) — прирост
исключительно от исправления метода замера, не от новых событий.

## 5. Живая нить: SeedDatabaseDSN failure-класс — без новых случаев

[live] 7д: весь failure-объём `SeedDatabaseDSN` по-прежнему на
`kkartov@yandex.ru` (актор — подтверждённый churn). Успехи 7д: kkartov,
`artemmendeleev`, `tarotreaderhimu`. Без изменений.

## 6. Chat→audit разрыв — 0% (7д: 18 vs 18)

Седьмой цикл подряд инструментирование чата держится.

## 7. UX-вывод — ЕДИНСТВЕННЫЙ ЦЕННЫЙ КАНДИДАТ ЦИКЛА

**Не паттерн отказа юзера — дефект инструмента, которым мы измеряем отказ.**
Каждый предыдущий цикл (минимум 6 подряд) писал вердикт "юзер молчит N часов"
на основе `MAX(audit_events.created_at)` без обязательного `actor_type='user'`.
Из-за смешанной семантики `actor_id` (system-действия иногда несут UUID
реального юзера-владельца ресурса, а не zero-UUID) это ЗАНИЖАЛО тишину на
десятки-сотни часов и минимум один раз (`sergeykozlov2006`) полностью
СКРЫЛО churn на 11+ дней — юзер не появлялся ни в одном отчёте, хотя ушёл
давно.

**Числа:** искажение 269.2ч на одном юзере, 21.6ч на другом; итог —
подтверждённый churn 30д-когорты вырос с 3 до 5 актёров без единого нового
события, только от смены метода.

**Кандидат в коде (root cause, не workaround):**
`build-agent/internal/db/builds.go:358-360` (BuildFinished) и
`build-agent/internal/db/deploy.go:354-362`, функция `handoffActor()`
(DeployImageVersion) — при `TriggeredBy == nil` (пуш/вебхук-сборка без живой
сессии) падают на `repo.CreatedBy` вместо zero-UUID. Паттерн правильного
поведения уже есть рядом в коде:
`backend/internal/api/audit.go:446-448` (`recordSystemAudit` →
`systemDeployActorID`, константа `backend/internal/api/deploy_hooks.go:1`) —
используется для `PlatformSelfHealRebuild`, но не для обычного
build-agent-пайплайна.

**Предложение в беклог:**
Заголовок (≤100 симв): "BuildFinished/DeployImageVersion пишут actor_id
владельца репо вместо zero-UUID при пуш-триггере — churn занижается на сотни часов"

Тело: `build-agent/internal/db/builds.go:358-360` и `deploy.go:354-362`
используют `COALESCE(triggered_by, created_by, zero-uuid)` — для автоматических
(push/webhook/self-heal) сборок это выбирает владельца репозитория, а не
zero-UUID, хотя `actor_type` уже корректно ставится в `'system'`. Итог:
любой запрос "последнее действие юзера" без явного `WHERE actor_type='user'`
путает автоматический редеплой с живым кликом. Минимум 2 из 5
подтверждённых churn-юзеров этого цикла были бы не найдены/недооценены без
ручного исправления метода. Фикс: заменить `COALESCE(triggered_by,
created_by, zero-uuid)` на `COALESCE(triggered_by, zero-uuid)` в обоих
местах (не подставлять `created_by` вообще для system-строк) — по аналогии
с уже работающим `recordSystemAudit`. Второй пункт: growth/audit-дашборды
(включая ЭТОТ файл в прошлых версиях) должны обязательно фильтровать
`actor_type='user'` при вычислении "последнего действия" — иначе баг воспроизводится
на уровне SQL даже после фикса записи (задел на будущее, старые строки в БД
уже искажены и не переписываются).

## 8. Сравнение с прошлым циклом (sess-0822g)

- Реальный сигнал появился впервые за 2 цикла: 75 user-строк вместо 0
  (§0) — активность artempro2022/artempro2021/michaelharlam/artemmendeleev.
- Churn-состав вырос 3→5, но НЕ из-за нового оттока — из-за исправления
  метода замера терминала (§4). Прошлые 6 циклов подряд писали
  `lifecoachrussia` как "растёт к границе 44→45ч" — это было систематически
  заниженное число, реальная тишина уже 74.9ч на момент прошлого цикла тоже
  была бы больше 48ч, если бы считали правильно.
- `sergeykozlov2006` — юзер, зарегистрированный 06-30, молчащий с 08-09, не
  фигурировал НИ В ОДНОМ предыдущем цикле разбора audit_events. Не новая
  находка о юзере — находка об инструменте измерения.
- Chat→audit разрыв 0% — 7-й цикл подряд без изменений.
- SeedDatabaseDSN failure-класс — без изменений.
