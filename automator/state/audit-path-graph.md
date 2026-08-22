# audit_events разбор — sess-0822g, 2026-08-22 03:1x-03:2x UTC

Гейт: `probe-prod-access.sh` зелёный (apiserver/readyz ok, psql exec
postgresql-0 ok, console 307). Все числа ниже — живой SQL через
`kubectl exec -n databases postgresql-0 -c postgresql -- psql "$DSN"`,
DSN из `argocd-prod/dada-cloud-console-backend#DATABASE_URL`, база
`cloud-console`. `select now()` = 2026-08-22 03:11:20 UTC.

## 0. Главный факт цикла: ОКНО МЕЖДУ ЦИКЛАМИ ПОЛНОСТЬЮ ТИХОЕ

[live] `select count(*) from audit_events where created_at >
'2026-08-22 02:05:07.97'` (max `created_at` прошлого цикла sess-0822f) = **1**,
и эта единственная строка — тот же `SendBuildNotification`
(`best-marriage-astrologer-in-guwahati`), который прошлый цикл уже видел
на границе окна (таймстемп `02:05:07.97386` > строки сравнения `.97` только
из-за микросекундного хвоста — это не новая запись, это та же самая).

Итог: **за ~1ч между циклами (02:05 → 03:11) в audit_events НЕ ПОЯВИЛОСЬ НИ
ОДНОЙ новой строки.** [live] `select id, email, created_at from users where
created_at > '2026-08-22 02:10:00'` — пусто, новых регистраций тоже нет.
Платформа не мертва (гейт зелёный, консоль отвечает 307), просто в этот
конкретный час никто ничего не делал. Разбор ниже — по прежнему 30-дневному
окну, которое почти не изменилось с прошлого цикла.

## 1. Новые юзеры

[live] `select count(*) from users where created_at > now() - interval
'30 days'` = **30** — без изменений численно четвёртый цикл подряд.
[live] Топ-10 по `created_at desc` подтверждает: последняя регистрация
по-прежнему `tarotreaderhimu@gmail.com` (08-21 13:58) — новых юзеров с
прошлого цикла нет.

Zero-audit среди когорты: [live] прямой join `audit_cnt/build_cnt/chat_cnt`
по всем 30 юзерам — **0 юзеров с нулём везде**, у каждого минимум 1
audit-строка (SignUp). Провалов инструментирования по этой оси нет.

### Находка цикла: скрытый staff-аккаунт в 30-дневной когорте
`michaelharlam@dada-tuda.ru` (created 2026-07-25, домен компании) сидит в
одной когорте с внешними юзерами и несёт 241 audit-строку + 107
chat-сообщений — это внутренний/тестовый аккаунт, не внешний сигнап,
искажает вид "30 новых юзеров" если его не отфильтровать. Граф путей ниже
считался с фильтром `email not like '%dada-tuda.ru%'`, но сам факт, что
такой аккаунт годами живёт неотличимо от внешнего в `users`, стоит на
заметке (см. §9).

## 2. Кто не сделал ничего / провалы инструментирования

Zero-audit: 0 (см. выше, прямой join, не только `NOT EXISTS`).

Chat→audit разрыв (`AgentChatUserMessage`) за 7д: **13 vs 13 = 0%** — пятый
цикл подряд 0%, инструментирование чата не деградировало.

## 3. Граф путей (30д, join по users, исключены `@dada-tuda.ru` и `system`)

Суммарно 4810 audit-строк за 30д на внешних юзеров (без изменений порядка
величины с прошлого цикла).

### Первое действие после SignUp
[live] `SessionStart` 12, `UploadSourceArchive` 2, `ViewProject` 1 —
меньше категорий, чем прошлый цикл показывал (там было 6 категорий на
похожем n) просто потому, что часть юзеров когорты имеет только 1
audit-строку (SignUp) без второго события — `rn=2` для них не существует.
Распределение первого ДЕЙСТВИЯ (не SignUp) не изменилось качественно:
`SessionStart` доминирует.

### Топ переходов action_A → action_B (self-loop исключён, n=20, 30д)
| A | B | cnt | distinct users |
|---|---|---|---|
| SessionStart | ViewProject | 223 | 10 |
| ViewProject | ViewApps | 202 | 12 |
| ViewApps | ViewApp | 102 | 8 |
| ViewApps | SessionStart | 98 | 8 |
| DeployImageVersion | DeployStack | 80 | 1 |
| BuildFinished | DeployImageVersion | 70 | 10 |
| ViewProject | SessionStart | 70 | 8 |
| DeployImageVersion | SessionStart | 58 | 5 |
| ViewApps | ViewProject | 54 | 8 |
| ViewApp | ViewApps | 43 | 7 |
| SetEnvVar | DeployImageVersion | 40 | 6 |
| DeployStack | DeployImageVersion | 39 | 1 |
| ViewBuildLogs | BuildFinished | 35 | 6 |
| ViewProject | ViewApp | 34 | 5 |
| SendBuildNotification | AutoscaleApp | 34 | 1 |
| SendNotification | SendBuildNotification | 32 | 1 |
| UploadSourceArchive | ViewBuildLogs | 32 | 4 |
| DeployImageVersion | SetEnvVar | 31 | 5 |
| AutoscaleApp | SendNotification | 30 | 1 |
| BuildFinished | CreateApp | 28 | 10 |

Здоровый путь (`ViewBuildLogs → BuildFinished`, `BuildFinished →
CreateApp` на 10 разных юзерах) остаётся доминирующей формой.

**Новая патология в топ-20, которой не было прошлый цикл:** пять рёбер
(`DeployImageVersion↔DeployStack`, `SendBuildNotification→AutoscaleApp→
SendNotification→SendBuildNotification`) с cnt 30-80, но **distinct
users = 1** — весь объём принадлежит одному тяжёлому юзеру
(`artempro2021@bk.ru`, 623 audit-строки за 30д, самый активный на
платформе). Это не новый паттерн юзеров, это один рабочий цикл одного
разработчика, который количественно перекрывает топ переходов. Тот же
класс искажения графа, что был у `SetDatabaseTier`-бёрста в прошлых
циклах — считать надо по distinct-users, не по cnt.

### Терминальное действие (30д-когорта, тишина >24ч, исключён `@dada-tuda.ru`)
| актор | терминал | часов тишины | metadata | классификация |
|---|---|---|---|---|
| lifecoachrussia@yandex.ru | DeployImageVersion | **44.0** | `gulyaev-ai-core`, success | ⚠️ ещё внутри 48ч, но выросло с 42.9ч прошлого цикла — граница близко |
| kkartov@yandex.ru | ViewApps | 55.8 | `apps:0, empty:true` | **churn** (без изменений) |
| good.win2283@gmail.com | ViewApps | 182.0 | `apps:1, healthy:1` | **churn** (без изменений) |
| cryocrm@gmail.com | ViewProject | 258.1 | `{}` | **churn** (без изменений) |
| + 15 ботов волны 08-08/09 | SessionStart | 309-319 | известная волна, не считается | — |

**Состав подтверждённого churn не изменился пятый цикл подряд** (kkartov,
good.win2283, cryocrm). `lifecoachrussia` пересекла из "42.9ч" в "44.0ч" —
если следующий цикл застанет её тишину >48ч без новой активности, она
переходит из "внутри окна" в четвёртый подтверждённый churn.

## 4. Живая нить №1: TriggerAutofix vs ViewBuildLogs — соотношение чуть выросло

[live] 30д: `TriggerAutofix` 9 (было 7) / `ViewBuildLogs` 92 (было 91),
distinct users по TriggerAutofix = 4. Прирост — 2 новые строки
08-21 23:16/23:26 от `artemmendeleev@gmail.com` (app `fonbet-value`):
первая `pending` (`client_claimed: "ui"`), вторая `failure` с
`status: 409, detail: "по этому приложению уже идёт автопочинка, ждите
завершения"`.

Проверил механизм по коду (`frontend/components/deploy/app-alerts-
banner.tsx`): кнопка блокируется локальным `useState` (`autofix.status`,
строки 528/546-555/735), а backend-текст 409 уже человекочитаем на
русском и попадает в `err.message` (строка 555) прямо в баннер — то есть
двойной запуск НЕ читается юзером как непонятная ошибка. Кнопка доезжает
до юзера (не архивная находка прошлых циклов про "недостижима со страницы
сборки" — это ЗАКРЫТО, `client_claimed: "ui"` подтверждает живой клик с
UI трижды за 30д на трёх разных юзерах: bruzas, lifecoachrussia,
artemmendeleev). Живая нить прошлых циклов закрыта практическим успехом
на новом юзере — фикс держится.

## 5. Живая нить №2: SeedDatabaseDSN failure-класс — новых случаев НЕТ

[live] 7д: `SeedDatabaseDSN` outcome — `failure` 172 / `success` 10,
**весь failure-объём всё ещё принадлежит одному актору kkartov@yandex.ru**
(`group by email` даёт единственную строку). За 4 цикла подряд ни один
другой юзер этот класс не задел. Диагноз не меняется: это специфичный для
kkartov ретрай-паттерн (актор ушёл 55.8ч назад, см. §3), не системная
угроза.

## 6. Сравнение с прошлым циклом (sess-0822f)

- Главное изменение: окно между циклами (~1ч) дало **0 новых audit-строк
  и 0 новых сигнапов** — платформа не мертва (гейт зелёный), просто
  наблюдательное окно пустое; разбор строится на 30-дневной когорте, а не
  на приросте.
- Churn-состав (kkartov, good.win2283, cryocrm) — без изменений 5-й цикл.
- Chat→audit разрыв — 0% 5-й цикл подряд.
- TriggerAutofix/ViewBuildLogs 7/91 → 9/92, +1 новый юзер
  (artemmendeleev) успешно воспользовался кнопкой (с одним 409-ретраем,
  корректно показанным).
- SeedDatabaseDSN failure-класс — без нового актора 4-й цикл подряд.
- Новая находка (не искалась прошлые циклы): staff-аккаунт
  `michaelharlam@dada-tuda.ru` сидит в 30-дневной когорте юзеров
  неотличимо от внешних (см. §9).
- Новая находка: пятирёберный кусок топ-20 графа переходов
  (`DeployImageVersion↔DeployStack`, `SendBuildNotification/
  AutoscaleApp/SendNotification`-цикл) целиком принадлежит одному
  юзеру (artempro2021, 623 строки) — искажает вид графа, если не
  фильтровать по distinct-users (тот же класс ошибки, что уже был с
  `SetDatabaseTier`-бёрстом system-актора).

## 7. UX-вывод / беклог — обязательный пункт

**Разрыв этого цикла — не UI-баг, а гейт данных.** У `users` нет
структурного признака "staff/test" аккаунта — единственный сигнал
`email like '%dada-tuda.ru%'`, применяемый ad-hoc в каждом SQL-запросе
разбора аудита. `michaelharlam@dada-tuda.ru` (241 audit-строка, 107
chat-сообщений, создан 07-25) целиком проходит как "новый внешний юзер"
в любом отчёте/дашборде, который не знает про этот фильтр — включая,
вероятно, admin/overview числа сигнапов и активности, если они считают
`users` без такого исключения.

**Проверил:** `grep -rn "is_staff\|is_internal\|account_type" backend/
internal/models/user.go` — совпадений нет, в модели `users` нет колонки
для этого различия.

**Предложение в беклог:**
Заголовок (≤100 симв): "Staff-аккаунты на @dada-tuda.ru неотличимы от
внешних юзеров в users/admin-метриках"

Тело: у `users` (`backend/internal/models/user.go`) нет поля
`is_internal`/`account_type`. Единственный способ отличить внутренний
аккаунт от внешнего сигнапа — текстовый фильтр по домену почты
(`email like '%dada-tuda.ru%'`), который каждый раз пишется заново в
ad-hoc SQL при разборе `audit_events`/`users`. Любая метрика активности
или сигнапов (включая, возможно, `/api/v1/admin/overview`, если он не
делает такой фильтр сам — не проверялось в этом цикле, см. `backend/
internal/api/admin.go` на предмет фильтра по домену) рискует посчитать
staff-тестирование как рост внешней базы. Пример живого случая:
`michaelharlam@dada-tuda.ru`, created 2026-07-25, 241 audit-строка + 107
chat-сообщений, сидит в одной когорте "30 новых юзеров за 30д" с реальными
сигнапами tarotreaderhimu/lifecoachrussia/kkartov. Фикс: добавить
булеву колонку `is_internal` в `users` (миграция) с дефолтом по домену
почты при создании, и явно исключать её во всех admin/growth-метриках,
а не полагаться на договорной email-паттерн в каждом разовом запросе.
