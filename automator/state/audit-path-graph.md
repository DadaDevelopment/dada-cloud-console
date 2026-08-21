# Разбор аудита: путь новых юзеров, граф переходов, деньги

Замер: sess-0821e, 2026-08-21 15:10 UTC. Гейт `probe-prod-access.sh` = ЗЕЛЁНЫЙ (apiserver /readyz=ok,
psql exec в `databases/postgresql-0` живой, консоль http=307). Все SQL — `kubectl exec -n databases
postgresql-0 -c postgresql -- psql "$DSN"`, DSN из секрета `argocd-prod/dada-cloud-console-backend`
(та же роль, что читает бэкенд).

Прошлый цикл закрыл окно на `now=2026-08-21 13:25 UTC` (файл этого же дня, см. git history). Дельта
этого цикла — **1 новый юзер** (`created_at >= '2026-08-21 13:25:00+00'`). 30-дневное окно пересчитано
целиком для проверки, что предыдущие выводы не устарели (граф/терминальные точки/фермерская волна не
изменились — 30 юзеров в 30д = 29 прошлые + 1 новый).

## 1. Новый юзер за дельту-окно (полная цепочка)

**`tarotreaderhimu@gmail.com`** (id `fa1cc1aa-2554-4d1f-ba72-7e6e2bd39ac4`), рег. 2026-08-21 13:58:01 UTC.
Полная цепочка из `audit_events` (19 строк, все реальные, не фарма):

```
13:58:01.13  SignUp              User            tarotreaderhimu@gmail.com        success
13:58:01.15  SessionStart        Session         tarotreaderhimu@gmail.com        success
13:58:01.73  CreateProject       Project         tarotreaderhimu-gmail-com        pending
13:58:03.00  ViewProject         Project         tarotreaderhimu-gmail-com        success
13:58:03.31  ViewApps            AppList         9eca0ca6-...                     success
13:58:15.50  StartGitAppInstall  git_installation github                          pending
13:58:21.78  FinishGitAppInstall git_installation github                          success
13:58:48.21  ConnectGitRepo      GitRepo         best-marriage-astrologer-in-guwahati  success
13:58:48.50  TriggerBuild        Build           best-marriage-astrologer-in-guwahati  success
13:58:48.80  ViewBuildLogs       Build           best-marriage-astrologer-in-guwahati  success
13:58:51.67  CreateProject       Project         tarotreaderhimu-gmail-com        success
14:00:20.67  BuildFinished       Build           best-marriage-astrologer-in-guwahati  failure
14:02:07.52  TriggerBuild        Build           best-marriage-astrologer-in-guwahati  success
14:03:36.82  BuildFinished       Build           best-marriage-astrologer-in-guwahati  failure
14:07:47.26  CreateServiceDatabase ServiceDatabaseV2 db-8f66797a                  pending
14:07:57.62  CreateServiceDatabase ServiceDatabaseV2 db-8f66797a                  success
14:08:05.52  TriggerBuild        Build           best-marriage-astrologer-in-guwahati  success
14:09:07.27  SeedDatabaseDSN     ServiceDatabaseV2 db-8f66797a                    success
14:09:36.49  BuildFinished       Build           best-marriage-astrologer-in-guwahati  failure
```

Это **лучший путь в окне**: не осмотр, а реальная попытка деплоя за 11 минут — signup → project →
git connect → 3× build, каждый упал на одной и той же стадии:

```
builds.error_message (все 3): dockerfile_build_failed: [build 5/6] RUN npm install: npm error ...
```

Между 2-й и 3-й попыткой юзер создал сервисную БД (`CreateServiceDatabase` → `SeedDatabaseDSN`),
похоже решил, что причина в отсутствующей базе — это не помогло, 3-я сборка упала на том же `npm
install`. Последнее событие 14:09:36 UTC, сейчас 15:10 UTC → **59 минут молчания, юзер ещё тёплый**
(<24ч), терминальным по определению задания (≥24ч) не считается, но живой обрыв цикла прямо сейчас.
Репозиторий `best-marriage-astrologer-in-guwahati` — судя по имени, SEO/контентный сайт, вероятно
чужой шаблон с непроходящим `npm install` (битый lock-файл/приватная зависимость) — это, скорее всего,
проблема репозитория юзера, не платформы, но пруфа этого утверждения (сам package.json) не читал —
это `unmeasured` в части «чья вина», измерен только факт: 3 попытки, 0 успехов, юзер ушёл сразу после
третьего провала.

## 2. Мёртвые сигнапы

Ноль новых юзеров дельты-окна = мёртвый сигнап. 30-дневное окно не изменилось: 0 из 30 юзеров имеют
ноль строк ВЕЗДЕ (все имеют хотя бы `SessionStart`), как и в прошлом цикле — диагноз "утечка до
первого действия" по-прежнему не воспроизводится.

## 3. Граф пути — без изменений по существу

Единственное новое ребро от дельты: `ConnectGitRepo -> TriggerBuild` (+1), `TriggerBuild ->
ViewBuildLogs` (+1), и новая тройка `BuildFinished(failure) -> TriggerBuild` (+2, ретраи), которой не
было в топ-40 прошлого цикла — **это первый живой пример «юзер ретраит после провала build», а не
уходит сразу**. Раньше граф видел только `BuildFinished -> DeployImageVersion` (успех) и `BuildFinished
-> CreateApp`; паттерн «упал → ретрай ещё раз → упал → ретрай снова → сдался» появился только сейчас.
Полный 30-дневный граф (переходы, доминирующий цикл осмотра `SessionStart → ViewProject → ViewApps →
ViewApp` без действия) не изменился — детали см. в предыдущей версии файла (git log
`automator/state/audit-path-graph.md`), здесь не дублирую, чтобы не раздувать файл дельта-циклом.

### Терминальные точки — топ-3 (полное 30д окно, ≥24ч молчания)
1. **`SessionStart` (фермерская волна 08-08, 14 юзеров)** — единственное событие, ~300ч молчания. Боты, не продукт.
2. **`ViewApps`/`ViewProject`** (5-6 реальных юзеров: `cryocrm@gmail.com` 244ч, `a.meshkov@dada-tuda.ru`
   235ч, `good.win2283@gmail.com` 168ч, `michaelharlam` 100ч) — люди уходят на осмотре консоли, ни разу
   не нажав "создать".
3. **`BuildFinished(failure)` после ретраев** (новый паттерн этого цикла, `tarotreaderhimu@gmail.com`,
   пока <24ч — кандидат на следующий цикл, если не вернётся) — первый живой кейс "упёрся в билд и ушёл",
   в прошлых циклах такого терминала не было зафиксировано вообще.

## 4. Деньги — 7 дней, статус после фиксов `3d6379f9` / `b49fe2a8`

```
payments (created_at >= now()-7d):
id       org_id  plan     amount  status    email                     inn         created_at (UTC)
1671e4a8 artempro2021@bk.ru business 2900.00 canceled  artempro2021@bk.ru        -           08-15 21:45 (уже был в прошлом отчёте)
eb4c8e48 dada    startup  990.00  canceled  sandbox-test@dada-tuda.ru 1234567894  08-18 12:42 (внутр. тест)
a295a6e6 dada    startup  990.00  canceled  alexkekiy@dada-tuda.ru    -           08-18 13:24 (внутр., владелец)
6ad2e12d dada    startup  990.00  canceled  alexkekiy@dada-tuda.ru    -           08-18 13:24 (внутр., владелец)
25f07e96 dada    business 2900.00 pending   -                         7807402712  08-19 21:01 (внутр. орг-счёт, pending — норм по c56a6ce1)
```

**Новых попыток оплаты от ВНЕШНЕГО (не dada/sandbox/owner) юзера за 7 дней НЕТ**, кроме уже известной
`artempro2021@bk.ru` от 08-15 (без изменений с прошлого цикла — не повторял). `succeeded`-платежей за
всё время по-прежнему один: `37a8d276` от 2026-07-25, тоже `org_id='dada'` без email/ИНН — внутренний.

**Разобрана путаница из аудита:** `audit_events` показывает `PaymentWebhook ... outcome=success` дважды
08-18 14:26 для платежей `a295a6e6`/`6ad2e12d`, которые сами при этом `status='canceled'`. Прочитал код —
это не баг данных, а неймспейс-коллизия термина: `backend/internal/api/billing_payments.go:236-256`
(`recordPaymentOutcomeAudit`) пишет `outcome=success` для ЛЮБОГО успешно обработанного вебхука (включая
`yookassa.OutcomeCanceled`), кроме `OutcomeUnknownPayment` (строка 240: `outcome :=
auditOutcomeSuccess`, флип на failure только `if result.Outcome == yookassa.OutcomeUnknownPayment`).
Поле `outcome` в этой audit-строке значит «вебхук обработан», а не «платёж прошёл» — при внешнем платеже
это будет читаться как ложный успех, если смотреть только на `audit_events.outcome` не открывая
`payments.status`. Не переименовываю сам (вне мандата этого файла), фиксирую как находку с местом.

**Главный вывод по деньгам без изменений:** `artempro2021@bk.ru` (самый активный юзер месяца, 611
audit-строк, 30 билдов) остаётся единственным реальным кандидатом с деньгами на столе и нулём
успешных платежей — без новых попыток с прошлого цикла, значит либо сдался, либо ждёт. Стоит списаться
напрямую, пока профиль ещё не остыл окончательно (последнее событие было 8.5ч назад на момент прошлого
замера — сейчас это уже больше суток, проверить свежий статус в следующем цикле).

## Беклог-кандидаты

### `PaymentWebhook` audit outcome=success маскирует отменённые платежи
`backend/internal/api/billing_payments.go:236-256` — `recordPaymentOutcomeAudit` ставит
`outcome=auditOutcomeSuccess` для webhook-исхода `OutcomeCanceled` (флип на failure только для
`OutcomeUnknownPayment`, строка 240). На проде 08-18 это дало 2 строки `PaymentWebhook ... success`
для платежей, которые сами `canceled` — при чтении графа аудита (как в этом самом разборе) это на
секунду читается как «оплата прошла». Для внутренних тестовых платежей это безобидно, но если так
случится с внешним клиентом, дежурный по логам увидит "success" и закроет тикет, хотя денег нет.
Предложение: развести `outcome=success` (вебхук обработан) и отдельное поле/значение для
`payment_status` в metadata аудита, либо минимум завести `outcome=neutral/processed` для canceled-ветки.

### Первый живой ретрай-после-провала billда без выхода к успеху
`tarotreaderhimu@gmail.com` (audit_events actor `fa1cc1aa-2554-4d1f-ba72-7e6e2bd39ac4`, 2026-08-21
13:58-14:09 UTC) — 3 билда подряд падают на одном и том же `RUN npm install` (repo
`best-marriage-astrologer-in-guwahati`), между попытками юзер создаёт сервисную БД, будто ищет причину
не там. Продукт не показывает юзеру, что ошибка стабильно повторяется на том же шаге — если бы
`ViewBuildLogs`/UI билда дедуплицировал/подсвечивал "тот же шаг падает 3-й раз подряд, вероятно не
инфраструктура" — юзер не тратил бы 11 минут на неверную гипотезу (создание БД). Кандидат в UX
беклог, не инженерный баг: нужен паттерн-детектор повторяющегося идентичного `error_message` на
одном `git_repo_id` с подсказкой юзеру. Пока <24ч, следующий цикл проверить — вернулся или ушёл
насовсем.
