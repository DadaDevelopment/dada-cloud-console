# Audit path graph — sess-0821f, 2026-08-21

Источник: psql в `cloud-console` через `kubectl exec pg-shard-0-postgresql-0 -n databases`
(creds-секрет `dada-cloud-console-backend` в ns `argocd-prod`, DB на pod `pg-shard-0-postgresql-0`,
НЕ на `postgresql-0` — тот отдельный шард). Момент снятия: `now()`=2026-08-21 17:08:07 UTC.
Прод достижим, туннель не мешал, `ensure-proxy.sh` сказал DIRECT-OK (прокси не понадобился).

## 1. Новые юзеры

- **24ч**: 1 юзер — `tarotreaderhimu@gmail.com` (рега 2026-08-21 13:58:01 UTC, source=awesome_webhosting, channel=yandex). Это тот же кейс из п.5.
- **30д (скользящее окно)**: 30 юзеров. Видна известная ферма-волна 08-08 (memory `project_signup_farm_wave_pollutes_funnel`): 15 из 30 юзеров зарегались в окне 19:49–22:56 UTC одного дня, случайные gmail/outlook/163.com/xyz-адреса.

## 2. Мёртвые сигнапы (30д когорта)

13 из 30 юзеров имеют **ровно 0 в builds/agent_chat_messages/feedback** — настоящие мёртвые сигнапы:
`17ffb57d-...@keycloak.local, bestmanskyline@gmail.com, chenlikun.18@gmail.com, clikuoo@gmail.com, dmimuser@outlook.com, dsoftru@yandex.ru, game@016818.xyz, grwang1201@outlook.com, langhakka9527@gmail.com, mail@ynotu.top, oddessc@outlook.com, zengqcyxx@gmail.com, zhisibi@163.com`.

12 из них — только `SessionStart`, ноль попыток что-либо сделать (чистая ферма/бот-трафик).
1 (`17ffb57d-...@keycloak.local`) дошёл до `AgentChatActionDeclined:connectGitRepo` — тронул продукт, но отказался/бросил, тоже 0 в builds/chat/feedback дальше.

**Важно**: ноль в audit ≠ ноль сигнала автоматически — перепроверено. У этих 13 действительно ноль везде (builds/chat/feedback), это не провал инструментирования, это реальный мёртвый сигнап или бот.

Остальные 17/30 юзеров имеют реальную активность (от 1 до 618 audit-событий).

## 3. Граф переходов (30д когорта, per-user consecutive audit events)

**Первое действие после регистрации** (top):
- `SessionStart` — 19 юзеров (дальше либо тишина, либо новый визит)
- `SignUp` — 6 (следующее событие уже другого типа, SignUp сам не всегда первая запись из-за таймингов)
- `CreateApp` — 2, `AgentChatActionDeclined` — 1, `CreateServiceDatabase` — 1, `RedeemPromo` — 1

**Терминальное действие** (последнее перед ≥24ч тишиной / последнее вообще):
- `SessionStart` — 19 (это в основном те же мёртвые сигнапы — залогинился и всё)
- `ViewApps` — 6, `DeployImageVersion` — 2, `AgentChatActionDeclined` — 1, `BuildFinished` — 1, `ViewProject` — 1

**Топ переходов A→B** (across все audit-события 30д когорты, n = событий, distinct_users):
```
SessionStart -> ViewProject          177 / 9 users
ViewProject  -> ViewApps             165 / 11 users
ViewProject  -> ViewProject          132 / 6 users   (повторные заходы)
ViewApps     -> SessionStart          76 / 7 users
ViewApps     -> ViewApp               68 / 6 users
ViewProject  -> SessionStart          66 / 6 users
ViewApps     -> ViewProject           57 / 7 users
UploadSourceArchive -> ViewBuildLogs  31 / 4 users
ViewApp      -> ViewApps              29 / 6 users
ViewBuildLogs -> BuildFinished        29 / 5 users
ViewApp      -> UploadSourceArchive   24 / 2 users
BuildFinished -> DeployImageVersion   24 / 4 users
ConnectGitRepo -> TriggerBuild        11 / 5 users
```
Читается так: основной живой цикл — `ViewProject ↔ ViewApps ↔ ViewApp`, воронка деплоя реальна
(`UploadSourceArchive → ViewBuildLogs → BuildFinished → DeployImageVersion`), но по объёму
навигация к приложению доминирует над попытками что-то собрать/задеплоить.

## 4. Деньги (7д)

5 попыток оплаты за 7д, все дедуплицированы по `payments.id`:

| org_id | status | amount | customer_email | комментарий |
|---|---|---|---|---|
| artempro2021@bk.ru | canceled | 2900 | artempro2021@bk.ru | owner платит себе |
| dada | pending | 2900 | (пусто, payer_inn=7807402712) | внутренняя org |
| dada | canceled | 990 | alexkekiy@dada-tuda.ru | owner |
| dada | canceled | 990 | alexkekiy@dada-tuda.ru | owner |
| dada | canceled | 990 | sandbox-test@dada-tuda.ru | тестовый аккаунт, org=dada (тоже owner alexkekiy) |

**Все 5 попыток за 7д — от владельца org или от sandbox-test внутри owner-org. Ноль попыток от настоящего чужого (non-owner) плательщика за 7д.**

За всё время в `payments`: 1 `succeeded` (org=dada, 990₽, 2026-07-25, тоже своя org, customer_email пуст) — это тоже не подтверждённая внешняя продажа, а внутренний прогон. `created_by_sub` пуст у ВСЕХ строк за 7д (см. memory `project_checkout_recorded_outcome_through_payers_own_context` — идентичность плательщика в чекауте и так ненадёжна).

**Вывод по деньгам: за 7д и за всё время не зафиксировано ни одной попытки оплаты от подтверждённо чужого (non-owner) покупателя, тем более успешной.**

## 5. Кейс `tarotreaderhimu@gmail.com` — числами

Рега 2026-08-21 13:58:01 UTC. Полная цепочка audit (19 событий, всё за ~11 минут):

```
13:58:01.13  SignUp
13:58:01.15  SessionStart
13:58:01.73  CreateProject          pending
13:58:03.00  ViewProject
13:58:03.31  ViewApps
13:58:15.50  StartGitAppInstall     pending
13:58:21.78  FinishGitAppInstall    success
13:58:48.21  ConnectGitRepo         best-marriage-astrologer-in-guwahati
13:58:48.50  TriggerBuild           success (build #1: 0ce47f4e)
13:58:48.80  ViewBuildLogs
13:58:51.67  CreateProject          success (запоздавшее success предыдущего pending)
14:00:20.67  BuildFinished          FAILURE #1 — dockerfile_build_failed / npm install
14:02:07.52  TriggerBuild           success (build #2: 207f9ae0)
14:03:36.82  BuildFinished          FAILURE #2 — тот же fail_reason, та же сигнатура (нормализованная)
14:07:47.26  CreateServiceDatabase  pending
14:07:57.62  CreateServiceDatabase  success (db-8f66797a) — попытался подключить БД, решив что дело в ней
14:08:05.52  TriggerBuild           success (build #3: 0f00460c)
14:09:07.27  SeedDatabaseDSN        success
14:09:36.49  BuildFinished          FAILURE #3 — тот же fail_reason, та же сигнатура
```

**Что делал ПОСЛЕ третьего провала**: ничего. `14:09:36.49` — последняя запись в audit для этого юзера, всего. Ноль событий дальше.

**Вернулся ли**: нет, по состоянию на снятие среза (17:08:07 UTC) — тишина **2ч58мин** после третьего провала. Это меньше 24ч-порога «терминальной тишины», окно наблюдения ещё не закрыто — рано писать «ушёл навсегда», но пока НЕ вернулся.

**Жив ли апп `best-marriage-astrologer-in-guwahati`**: да, строка в `git_repos` (id `efb5f77b-a53f-4f44-80bf-ff0d15f64a83`) существует, `DeleteApp` не вызывался. Апп висит в состоянии «3/3 билда failed», не задеплоен ни разу.

**Сигнатура провала все 3 раза одинаковая по сути** (`fail_reason='dockerfile_build_failed'`, `error_message` начинается с `[build 5/6] RUN npm install: npm error`), но **строкового совпадения НЕТ** — `error_message` содержит embedded-таймстемп лог-файла (`/root/.npm/_logs/2026-08-21T13_59_54_133Z-debug-0.log` и т.п.), уникальный на каждый прогон. Точное сравнение строк даёт 0 повторов; нормализованное (regex вырезает таймстемп) — даёт 3/3 совпадения. **Это отдельная находка: если фича мерит повторы точным сравнением `error_message`, она недосчитывает 100% таких случаев.**

### База по всей платформе: серии 2+ подряд одинаковых провалов на одном апп/репо, 30д

Считал по нормализованной сигнатуре (`fail_reason` + `error_message` с вырезанным `ISO-таймстемп` в пути лога), т.к. точное строковое сравнение занижает счёт (см. кейс tarot выше).

- Всего репо с билд-активностью за 30д: **15**.
- Репо, где была серия из 2+ подряд провалов с одинаковой (нормализованной) сигнатурой: **4** (27% активных репо).
- Затронутых юзеров: **4** (по одному репо на юзера).

| app_name | owner | repeat-события | окно | исход |
|---|---|---|---|---|
| best-marriage-astrologer-in-guwahati | tarotreaderhimu@gmail.com | 3 (все) | 2026-08-21 13:58–14:08 | **застрял** — 3/3 failed, тишина 2ч58м на момент среза, апп жив, не задеплоен |
| gulyaev-ai-core | lifecoachrussia@yandex.ru | 6 | 2026-08-19 09:39 – 2026-08-20 07:15 | **success** — выбрался сам после серии, последний билд success 08-20 07:15 |
| agent-orchestrator-ui | alexkekiy@dada-tuda.ru (owner, внутренний тест-аккаунт) | 4 | 2026-08-02 – 2026-08-12 | success — выбрался, исключить из продуктового счёта (staff) |
| a2ahub-landing | tech@xn--d1acaa3cs0b.xn--p1ai | 3 | 2026-08-04 – 2026-08-12 | success — выбрался (08-12 14:03) |

**Итого без staff-аккаунта (agent-orchestrator-ui — это owner alexkekiy@dada-tuda.ru, внутренний): 3 реальных внешних юзера за 30д словили серию 2+ повторных провалов. 2 из 3 выбрались сами (success через 1 день / 8 дней), 1 (tarot, сегодняшний) пока завис — самый свежий случай, ещё в пределах окна наблюдения, не подтверждён как потерянный.**

## Кандидаты в беклог

### Счётчик повторных провалов сборки сравнивает error_message как есть, теряя все реальные повторы
`fail_reason` совпадает у последовательных провалов, но `error_message` содержит встроенный таймстемп путей npm-лога (`/root/.npm/_logs/<ISO8601>-debug-0.log`), уникальный на каждый прогон. Любая будущая метрика/алерт/фича «юзер застрял в повторе», которая сравнивает `error_message` строкой в строку, даст 0 срабатываний там, где сигнатура на самом деле идентична (кейс tarotreaderhimu 21.08 — 3/3 провала с одинаковой причиной, точное сравнение находит 0 повторов). Нормализовать сравнение (обрезать/вырезать таймстемп-путь) до того, как строить любую метрику или auto-fix триггер на этом поле.

### У trigger'а auto-fix/подсказки после повторного провала нет — юзер решает проблему наугад и теряет апп
tarotreaderhimu 21.08: после 2-го провала юзер сам предположил «дело в базе», создал ServiceDatabaseV2 и заново задеплоил (14:07–14:09) — 3-й билд упал с ТОЙ ЖЕ причиной (`npm install`), т.е. гипотеза юзера была неверной, а продукт не подсказал, что причина не в БД. Апп жив, не задеплоен, юзер молчит 2ч58м на момент среза. Разово это рано считать потерей (окно тишины <24ч), но паттерн виден и на двух других юзерах за 30д (gulyaev-ai-core, a2ahub-landing) — оба тоже гоняли повторные билды вслепую, просто им повезло больше попыток до успеха. Основа для фичи: на 2-м подряд провале с одинаковой (нормализованной) сигнатурой показывать точную причину/лог-строку, а не давать юзеру гадать.

### `payments.created_by_sub` пуст на 100% строк за 7д — атрибуция плательщика не работает вообще
Ни одна из 5 записей `payments` за последние 7 дней не несёт `created_by_sub`. Это не позволяет достоверно отличить «оплата от owner» от «оплата от чужого» иначе как через `org_id`/`customer_email` эвристику (которая тоже не всегда надёжна, см. memory `project_checkout_recorded_outcome_through_payers_own_context`). Если бизнес хочет мерить конверсию НЕ-owner плательщиков, эта колонка должна реально заполняться на чекауте — сейчас измерить долю чужих платежей нельзя, только косвенно через org_id/customer_email.
