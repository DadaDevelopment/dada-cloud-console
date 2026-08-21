# Audit path graph — sess (второй разбор, дельта), 2026-08-21 17:37 UTC

Источник: psql в `cloud-console` через `kubectl exec pg-shard-0-postgresql-0 -n databases`
(creds `dada-cloud-console-backend` в ns `argocd-prod`), доступ через `vpn-bypass-proxy.py`
(en0-туннель у утренней сессии был мёртв, у этой — жив после рестарта прокси на порту 8899;
`ensure-proxy.sh` сам сказал PROXY-DEAD и отказался выдать HTTPS_PROXY, но ручной рестарт
`vpn-bypass-proxy.py --port 8899` прошёл, kubectl/psql подтверждены живьём). Момент снятия:
`now()`=2026-08-21 17:37:28 UTC. READ-ONLY: ни одного UPDATE/INSERT/DELETE не выполнялось.

Прошлый срез: `sess-0821f`, 2026-08-21 17:08:07 UTC — те же 30 минут назад, тот же 30-дневный
скользящий когорт (первый юзер в окне — 07-23, ни один не выпал, только 1 новый добавился —
tarotreaderhimu, он уже был в прошлом срезе). Из-за этого раздел 1-3 почти не меняется числом,
но раздел выявил РЕАЛЬНУЮ дельту: неучтённых мёртвых сигнапов и, главное, недоставленный фикс.

## 1. Новые юзеры за скользящие 30 дней

- **24ч**: 1 юзер — `tarotreaderhimu@gmail.com` (без изменений от прошлого среза).
- **30д (скользящее окно)**: 30 юзеров, тот же список, что и в прошлом срезе (окно почти не
  сдвинулось: 30 минут между снятиями, самый старый юзер в когорте — 07-23, есть запас).
  Полная цепочка `(created_at, action, resource_kind, resource_name)` для каждого — как и раньше,
  доминирует `SessionStart → ViewProject → ViewApps` навигация, разбор ниже.

## 2. Кто не сделал НИЧЕГО (перепроверено по builds/git_repos/agent_chat_messages/feedback)

Перепроверка через LEFT JOIN на все 4 таблицы (`builds.triggered_by`, `git_repos.created_by`,
`agent_chat_messages.user_sub`/`feedback.user_sub` = `users.keycloak_sub`):

**НОВОЕ по сравнению с прошлым срезом**: прошлый разбор насчитал 13/30 мёртвых сигнапов
(порог — `audit_cnt=1`, чистый `SessionStart`). Перепроверка нашла ещё **2 юзеров**, у которых
в audit ровно `SessionStart` **дважды** (повторный визит), но по-прежнему ноль в
builds/agent_chat_messages/feedback/git_repos: `a@atry.kdns.fr`, `abc@zhkarc.us.ci` — оба из
известной ферма-волны 08-08 (регистрация 20:29 и 22:52 UTC того дня). Прошлый порог
`audit_cnt=1` их пропустил, потому что они зашли дважды, а не один раз.

**Итог: 15/30 мёртвых сигнапов** (а не 13/30, как в прошлом срезе) — это уточнение счёта той же
когорты, не появление новых юзеров. Все 15 — чистая ферма/бот-навигация, `SessionStart` и
ничего больше.

**Провал инструментирования (audit=0, но есть активность в других таблицах)**: явный запрос
`NOT EXISTS (audit_events)` по всей 30д-когорте вернул **0 строк** — ни один юзер в этой когорте
не имеет активности при нулевом audit. В этот раз новой дыры в телеметрии на уровне «юзер
что-то сделал, а audit молчит» не найдено (в отличие от прошлых циклов, см. memory
`project_audit_events_silently_drops_rows.md` — это НЕ отменяет прошлую находку, просто в
ТЕКУЩЕЙ когорте её сейчас нет).

## 3. Граф пути (30д когорта)

**Первое действие после регистрации** (не изменилось от прошлого среза):
```
SessionStart               19
SignUp                      6
CreateApp                   2
AgentChatActionDeclined     1
CreateServiceDatabase       1
RedeemPromo                 1
```

**Терминальное действие, порог ≥72ч тишины** (пересчитано с правильным порогом задачи —
прошлый разбор использовал 24ч, здесь 72ч, поэтому числа не сравнимы напрямую):
```
SessionStart              18  (терминально, ≥72ч тишины)
ViewApps                    3
AgentChatActionDeclined     1
ViewProject                 1
```
23 из 30 юзеров когорты пересекли порог 72ч тишины (то есть точно "сдались" на срез этого
момента); 7 из 30 ещё в окне наблюдения (последнее событие <72ч назад, рано звать "ушёл").

**Топ переходов A→B** (все audit-события 30д когорты, n=событий, distinct_users):
```
SessionStart -> ViewProject          177 / 9 users
ViewProject  -> ViewApps             165 / 11 users
ViewProject  -> ViewProject          132 / 6 users
ViewApps     -> SessionStart          76 / 7 users
ViewApps     -> ViewApp               68 / 6 users
ViewProject  -> SessionStart          66 / 6 users
ViewApps     -> ViewProject           57 / 7 users
ViewApps     -> ViewApps              48 / 5 users
SessionStart -> SessionStart          38 / 9 users
UploadSourceArchive -> ViewBuildLogs  31 / 4 users
ViewApp      -> ViewApps              29 / 6 users
ViewBuildLogs -> BuildFinished        29 / 5 users
BuildFinished -> DeployImageVersion   24 / 4 users
```
Не изменилось качественно: навигация `ViewProject↔ViewApps↔ViewApp` доминирует, воронка
деплоя реальна, но меньше по объёму.

**Аномалия графа, найденная в этот раз**: `SeedDatabaseDSN -> SeedDatabaseDSN` (168 раз, 1
юзер) и `VerifyDomainAuthorization -> VerifyDomainAuthorization` (31 раз, 1 юзер) — обе от
`kkartov@yandex.ru`. Разбор ниже (раздел 4) — это НЕ новая живая проблема, это уже закрытый
инцидент 08-19, но граф его до сих пор показывает как самую частую пару переходов в датасете
после базовой навигации, так что стоит явно объяснить, а не оставить как непонятную аномалию.

## 4. Найденный (и уже закрытый) инцидент: retry-storm на `SeedDatabaseDSN`

`kkartov@yandex.ru` (30д-когорта, второй по активности юзер, 513 audit-событий) словил **172
подряд провала `SeedDatabaseDSN`** за 2026-08-18 22:06–22:34 UTC (раз в 10 секунд, `trigger:
auto`, `app_ref: instatic-il1cvo`), все с одинаковой ошибкой:

```json
{"error": "decoding encryption key: encoding/hex: invalid byte: U+000A", "app_ref": "instatic-il1cvo", "trigger": "auto"}
```

`U+000A` = `\n` — хвостовой перенос строки в `GITOPS_ENCRYPTION_KEY` ломал hex-декодинг, и
асинхронный доставщик DSN (`deliverDatabaseDSNAsync`) ретраил permanent-фейл как будто он
transient, раз в 10 секунд, без остановки. Тот же корень дал 5 подряд failed builds на app
`instatic` в ночь на 08-19 (`fail_reason=platform_error`, 22:46–03:17 UTC) — это НЕ отдельная
серия повторных провалов, это тот же инцидент виден с другой стороны (build-триггер зависел от
того же ключа).

**Уже пофикшено и уже в проде**: `backend/internal/crypto/crypto.go:16-34` — коммит
`17db736d fix(crypto): survive whitespace in GITOPS_ENCRYPTION_KEY and git credentials`,
2026-08-19 15:43 MSK. Введён `ErrKeyMisconfigured` — маркер "это конфиг сломан навсегда, а не
временный сбой", ретрай-петля обязана остановиться при такой ошибке. Проверено:
`git merge-base --is-ancestor 17db736d... 3560cd90` → **LIVE в текущем деплое** (см. раздел 6).
Инцидент закрыт до этого разбора, приводится здесь только для объяснения графа. kkartov жив
(последняя активность 08-19 19:26 UTC, тишина 1д22ч на момент среза, не терминальна).

## 5. Кейс `tarotreaderhimu@gmail.com` — статус на 17:37 UTC

Полная цепочка не изменилась (19 событий, последнее — `BuildFinished FAILURE #3` в 14:09:36.49).
**Тишина на момент этого среза: 3ч23м59с** (было 2ч58м на прошлом срезе). Юзер НЕ вернулся.
Порог 24ч ещё не пройден, порог 72ч тем более — писать "ушёл навсегда" по-прежнему рано, окно
наблюдения открыто. Апп `best-marriage-astrologer-in-guwahati` жив, не задеплоен, `DeleteApp` не
вызывался.

**Новых build-активностей за последние 2 часа на платформе — 0** (кроме двух success-билдов
`fonbet-value`, не относящихся к когорте). Новых серий повторных провалов с момента прошлого
среза не появилось.

## 6. ГЛАВНАЯ находка этого цикла: фикс написан, запушен, но НЕ доставлен в прод

Прошлый разбор (`sess-0821f`) поднял в беклог два пункта:
1. счётчик повторов сравнивает `error_message` буквально и теряет реальные повторы;
2. после 2-го подряд провала с одинаковой причиной продукт не подсказывает юзеру, что он
   гоняет билды вслепую.

**Оба пункта закрыты кодом** в этой же сессии-владельце между срезами:
- `50e773cd` "Три одинаковых провала сборки выглядели для юзера как три разных, и он ушёл
  чинить не то" — backend `backend/internal/api/build_repeat.go` (нормализованная
  `failureSignature`, режет ISO-таймстемпы/uuid/hex из `error_message` перед сравнением) +
  frontend `frontend/lib/build-repeat.ts` (`isStuckOnRepeat`, `repeatHintKey`) и
  `frontend/components/deploy/app-latest-build-card.tsx:138,293,364-366` (баннер "второй подряд
  провал с той же причиной" + адресный хинт по `fail_reason`).
- `0fd0eaaa` "Цикл записан: разбор аудита поймал, что фича мерила бы ноль на своём же кейсе" —
  докрутка того же фикса.

Проверено, что рычаг реально достижим (не мёртвый код, см. memory
`project_shipped_lever_can_be_structurally_unreachable.md`): `repeat_count` в JSON API
(`backend/internal/api/builds.go:44,170,240,245`), и фронт реально его читает и рисует —
`grep repeat_count frontend/lib/types.ts frontend/lib/build-repeat.ts frontend/lib/build-repeat.test.ts
frontend/components/deploy/app-latest-build-card.tsx` — все 4 файла есть, не заглушка.

**НО** — проверка по правилу "закрыт коммитом ≠ закрыт доставкой" (git ancestry против
реального образа в кластере, не текст коммита):

```
origin/main HEAD           = 0fd0eaaa (несёт оба фикса)
деплой в проде сейчас       = ghcr.io/dadadevelopment/dada-cloud-console-backend:3560cd90
                               ghcr.io/dadadevelopment/dada-cloud-console-frontend:3560cd90
git merge-base --is-ancestor 50e773cd 3560cd90  →  NOT an ancestor
```

Деплой в кластере (`argocd-prod`, поды `dada-cloud-console-backend-785fb7df59-*` /
`dada-cloud-console-frontend-57644bc4d7-*`, обновлены 36 минут назад) стоит на теге `3560cd90` —
**на два коммита позади** `50e773cd`/`0fd0eaaa`. Фикс полностью написан, протестирован (есть
`build_repeat_test.go`, `build_repeat_db_test.go`), запушен в `origin/main`, но живой tarot-кейс
из раздела 5 **прямо сейчас** видит старую версию карточки билда без баннера "второй подряд
провал" — потому что новый образ ещё не выкатился. Если бы деплой прошёл раньше, у tarot после
2-го провала (14:03:36 UTC) уже был бы виден баннер с хинтом "npm install won't fix itself on
retry" вместо того, чтобы он гадал и создавал ненужную базу в 14:07.

**Действие**: это не "написать код" (код готов), это "выкатить `origin/main` в `argocd-prod`" —
следующий деплой console-backend/console-frontend автоматически подтянет `0fd0eaaa`, обычный
ArgoCD sync/redeploy, без ручных правок кода.

## 7. Деньги (7д) — перепроверено

Те же 5 попыток оплаты, что и в прошлом срезе, ни одной новой:

| org_id | status | amount | customer_email | created_by_sub |
|---|---|---|---|---|
| dada | pending | 2900 | (пусто) | (пусто) |
| dada | canceled | 990 | alexkekiy@dada-tuda.ru | (пусто) |
| dada | canceled | 990 | alexkekiy@dada-tuda.ru | (пусто) |
| dada | canceled | 990 | sandbox-test@dada-tuda.ru | (пусто) |
| artempro2021@bk.ru | canceled | 2900 | artempro2021@bk.ru | (пусто) |

`created_by_sub` пуст на 100% строк за 7д — не изменилось, всё ещё не позволяет отличить
владельца от чужого плательщика без эвристики по `org_id`/`customer_email`. Ноль попыток
оплаты от подтверждённо-чужого плательщика за 7д, как и на прошлом срезе.

## Кандидаты в беклог

### [АКТИВНЫЙ] Фикс "второй подряд провал = баннер с хинтом" готов, но не выкачен в прод
Коммиты `50e773cd`+`0fd0eaaa` на `origin/main` полностью решают backlog-пункт прошлого цикла
(нормализованное сравнение `error_message`, UI-баннер `frontend/components/deploy/app-latest-build-card.tsx:293,364-366`
с адресным хинтом по `fail_reason` из `frontend/lib/build-repeat.ts:37-44`). Деплой в
`argocd-prod` стоит на `3560cd90`, на 2 коммита позади. Живой кейс tarotreaderhimu (21.08,
3/3 провала, потерял 2ч на угадывание причины) НЕ увидел бы этот баннер даже сегодня —
выкатки не было. Нужен обычный redeploy `dada-cloud-console-backend`+`dada-cloud-console-frontend`
из `origin/main`, кода менять не надо.

### [Информационно, не требует действия] Порог "мёртвый сигнап" по ровно 1 audit-событию недосчитывает повторные визиты
Прошлый разбор считал мёртвым сигнапом только `audit_cnt=1`; уточнение нашло ещё 2 юзеров с
`audit_cnt=2` (два `SessionStart`, ноль дальше) в той же ферма-волне 08-08 — итог 15/30, не
13/30. Не баг, просто будущим разборам мёртвых сигнапов стоит считать по "все события —
SessionStart", а не по фиксированному count=1.

### [Закрыто, для памяти] Retry-storm на `SeedDatabaseDSN` из-за хвостового `\n` в ключе шифрования
172 автоматических провала за 28 минут 08-18/08-19 у `kkartov@yandex.ru`, `app_ref=instatic`.
Уже исправлено `17db736d` (`backend/internal/crypto/crypto.go:16-34`, `ErrKeyMisconfigured`),
подтверждено живым в текущем деплое (`3560cd90` — фикс попал раньше build-repeat фикса).
Оставлено в отчёте только чтобы объяснить аномальную пару переходов графа
`SeedDatabaseDSN→SeedDatabaseDSN` (168/1) — не открывать заново.

### [Без изменений от прошлого цикла] `payments.created_by_sub` пуст на 100% строк
Не тронуто в этом цикле, тот же разрыв — чекаут не пишет актора платежа, атрибуция
владелец/чужой держится только на `org_id`/`customer_email` эвристике.
