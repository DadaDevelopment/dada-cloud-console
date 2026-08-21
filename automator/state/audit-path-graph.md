# Разбор часового аудита: путь новых юзеров, граф переходов, деньги

Замер: 2026-08-21, окно 30 дней (`created_at >= now()-30d`, now = 2026-08-21 13:25 UTC).
Сеть до прода зелёная весь прогон (`kubectl get nodes` DIRECT-OK, без прокси). Все SQL — `kubectl exec -n databases pg-shard-0-postgresql-0 -- psql -U svc-cloud-console -d cloud-console` (роль `gateway_ro` не имеет прав на `users` — использован `svc-cloud-console`, креды из секрета `dada-cloud-console-backend` ns `argocd-prod`). Метрика — Reporting API, токен из `.secrets`, counter 110158915.

## 1. Новые юзеры за 30 дней — [live]

**29 новых строк в `users`** (28 реальных + 1 `service-account-dada-eval-svc`, исключён из выводов ниже). Все 29 имеют ≥1 строку в `audit_events` — класс «ноль ВЕЗДЕ» (мёртвый сигнап без единого следа) в этом окне **не встретился ни разу**. Это меняет диагноз с прошлых циклов: раньше искали «умер до первого действия», сейчас проблема дальше по воронке.

Полная цепочка `(created_at, action, resource_kind, resource_name)` по каждому юзеру — 611 строк у топ-юзера, полный дамп в `/private/tmp/claude-501/.../tool-results/bv415ikrz.txt` (эфемерный путь сессии, не хранить как источник правды). Сюда — сжатая картина.

### Три класса по активности (п.2 задания)

**Класс A — фермерская волна 08-08, 14 юзеров, действие = только `SessionStart`, дальше тишина.** Один и тот же час (19:49–20:29 UTC), почтовые домены `outlook/gmail/163.com/atomicmail/zhkarc.us.ci` — сигнатура зачищена ранее как `project_signup_farm_wave_pollutes_funnel`. Ни один не сделал `ViewProject`/`ViewApps`. Это не провал инструментовки — это боты, у которых `SessionStart` (единственное событие) есть у всех, значит трекинг регистрации живой, просто нечего мерить дальше.

**Класс B — реальные юзеры, ноль в `audit_events` кроме входа, НО активность в другой таблице → ПРОВАЛ ИНСТРУМЕНТИРОВАНИЯ (главная находка цикла).**

`macmam@atomicmail.io` (рег. 2026-08-08 18:28): в `audit_events` только 3×`SessionStart`. В `agent_chat_messages` — 34 сообщения (2026-08-08 20:42–20:54), реальный диалог на фарси: юзер просит продиагностировать деплой app «9router», агент вызывает `getAppState`/`listApps`/`searchLogs` и **отдаёт живые данные**: `listApps` в 20:43 возвращает app `id=3c2ae1fe-e931-4a0c-a238-c88f5080a3fe`, `project_id=9008a656-6373-4ab8-b9cf-6dbf87544f70`, фаза Ready; `searchLogs` отдаёт 18 реальных строк логов с таймстампами `2026-08-08T19:54:21Z`.

Проверено по всем таблицам — **у этого проекта/аппа/окружения нет ни одной строки нигде в БД**:
```
projects        where id='9008a656-...'          → 0 rows
environments    where project_id='9008a656-...'   → 0 rows
environments    where id='1d628085-...' (из getProject) → 0 rows
git_repos       where project_id='9008a656-...'   → 0 rows
builds          where app_name='9router'          → 0 rows
audit_events    where project_id='9008a656-...'   → 0 rows
audit_events    where resource_name='9router'     → 0 rows
projects        where owner_id=<macmam.id>         → 0 rows (у юзера НЕТ ни одного проекта в БД вообще)
```
При этом инструмент `listApps` в 20:49 и 20:53 (те же project/user) уже отдаёт `apps:[]` — то есть между 20:43 и 20:49 живой ответ схлопнулся в пустоту, а Postgres не видел этот проект НИКОГДА (не «удалили между вызовами» — «не было записи ни до, ни после»).

Вывод: агентский тул-путь (`listProjects`/`listApps`/`getAppState`/`searchLogs`) читает состояние не через строку в `projects`/`git_repos`, а откуда-то ещё (вероятно живой k8s/кэш), и способен отдать юзеру полностью оформленный проект с приложением и логами, для которого **в системе учёта нет ни одного канонического ряда** — ни владения, ни аудита, ни билда. Это может быть: (а) чужой тенант, чей namespace/лейбл совпал по неймингу с автосозданным `macmam-atomicmail-io` (имя видно в `listProjects`: `"name":"macmam-atomicmail-io"`, паттерн авто-провижининга по email — так что скорее всего СВОЙ проект юзера, просто его строка в БД не пережила создание/была снесена), либо (б) провижининг при регистрации пишет в k8s раньше/без записи в Postgres, и что-то (авто-чистка фермерской волны 08-08, которая шла в тот же час) снесло Postgres-строки, не тронув живой namespace на несколько минут. В любом случае: юзер получил через агент-чат данные о ресурсе, который официальный аудит и биллинг не видят вообще — дыра и в измеримости, и потенциально в изоляции тенантов.

`good.win2283@gmail.com`: `audit_cnt=92`, но `agent_chat_messages`=4 — здесь наоборот, чат почти не использован, расхождение небольшое, не тянет на отдельный класс.

**Класс C — нормальный путь, есть в audit** — 12 юзеров с реальной последовательностью действий (см. граф ниже), включая топ-активного `artempro2021@bk.ru` (611 audit-строк, 30 билдов, 2 git-репо, платежи — детали в п.5).

## 2. Мёртвые сигнапы

**Ноль юзеров этого окна — «мёртвый сигнап» (ноль везде).** Все 29, включая фермерскую волну, оставили хотя бы `SessionStart`. Прошлый диагноз «утечка до первого действия» здесь не воспроизводится — по крайней мере `SessionStart` пишется надёжно.

## 3. Граф пути — [live]

### Первое действие СРАЗУ ПОСЛЕ регистрации (первая audit-строка каждого нового юзера)
| первое действие | юзеров |
|---|---|
| SessionStart | 19 |
| SignUp | 5 |
| CreateApp | 2 |
| AgentChatActionDeclined | 1 (eval SA, исключить) |
| CreateServiceDatabase | 1 |
| RedeemPromo | 1 |

### Топ переходов `action_A -> action_B` (count / distinct юзеров), полная выборка в query, топ-40 приведён
```
SessionStart      -> ViewProject          174 / 9
ViewProject       -> ViewApps             161 / 10
ViewProject       -> ViewProject          132 / 6
ViewApps          -> SessionStart          74 / 6
ViewApps          -> ViewApp               68 / 6
ViewProject       -> SessionStart          66 / 6
ViewApps          -> ViewProject           57 / 7
ViewApps          -> ViewApps              48 / 5
SessionStart      -> SessionStart          36 / 9
UploadSourceArchive -> ViewBuildLogs       31 / 4
ViewApp           -> ViewApps              29 / 6
ViewBuildLogs     -> BuildFinished         29 / 5
BuildFinished     -> DeployImageVersion    24 / 4
ViewApp           -> UploadSourceArchive   24 / 2
ViewApp           -> SessionStart          20 / 4
ViewProject       -> ViewApp               20 / 4
DeployImageVersion -> ViewProject          16 / 5
DeployImageVersion -> DeployImageVersion   15 / 5
ViewBuildLogs     -> ViewApps              12 / 4
ViewApp           -> ViewProject           12 / 5
DeleteApp         -> DeleteApp             11 / 2
ConnectGitRepo    -> TriggerBuild          10 / 4
ViewProject       -> ViewBuildLogs         10 / 2
TriggerBuild      -> ViewBuildLogs          9 / 5
BuildFinished     -> CreateApp              8 / 5
StartGitAppInstall -> ConnectGitRepo        8 / 2
TriggerBuild      -> BuildFinished          7 / 2
```
Читай как маршрут: доминирующий цикл — `SessionStart → ViewProject → ViewApps → ViewApp` (осмотр, без действия), меньшинство доходит до `UploadSourceArchive → ViewBuildLogs → BuildFinished → DeployImageVersion` (реальная доставка). Из 12 «класс C» юзеров цикл осмотра БЕЗ деплоя проходят минимум 6-7 — воронка теряет людей на «посмотрел и ушёл», не на техническом отказе.

### Терминальные точки (последнее действие, тишина ≥72ч на момент замера)
23 из 29 юзеров терминальны (≥72ч молчания), 6 ещё «тёплые» (<72ч):

| ещё активны (<72ч) | последнее действие | часов молчания |
|---|---|---|
| michaelharlam@yandex.ru | SessionStart | 1.3 |
| artempro2022@yandex.ru | DeployImageVersion | 8.4 |
| artempro2021@bk.ru | DeployImageVersion | 8.5 |
| lifecoachrussia@yandex.ru | DeployImageVersion | 30.2 |
| kkartov@yandex.ru | ViewApps | 42.0 |
| mytake@yandex.ru | ViewApps | 48.0 |

| терминальны (≥72ч), точка сдачи | последнее действие | часов молчания |
|---|---|---|
| 14× фермерская волна 08-08 | SessionStart (единственное) | 296–306 |
| cryocrm@gmail.com | ViewProject | 244.3 |
| a.meshkov@dada-tuda.ru | ViewApps | 235.5 |
| good.win2283@gmail.com | ViewApps | 168.2 |
| michaelharlam (username) | ViewApps | 100.4 |

Точка сдачи для реальных (не-ферма) терминальных юзеров — **`ViewApps`/`ViewProject`**, то есть люди уходят на этапе осмотра консоли, ни разу не дойдя до `CreateApp`/`UploadSourceArchive`. Единственное исключение — `macmam@atomicmail.io` (класс B выше), терминален на `SessionStart` в audit, но реально ушёл после диалога в агент-чате про несуществующий в БД деплой.

## 4. Аттрибуция (Метрика → audit → build → payment) — частично unmeasured

Живые reaches за 30д (`ym:s:goal<id>reaches`, счётчик 110158915, окно 2026-07-22..2026-08-21):

| цель | reaches |
|---|---|
| register-url (585010094) | 222 |
| registration_complete (586052031) | 7 |
| deploy_success (585205874) | 29 |
| signup_started (593177849) | 95 |
| auth_callback_failed (593177850) | 24 |

**НАХОДКА:** `registration_complete`=7 (события, не люди, помню про ловушку goal-reaches) против 28 реальных новых строк в `users` за то же окно — JS-цель ловит в разы меньше завершённых регистраций, чем реально появляется в БД. Одновременно `auth_callback_failed`=24 против `registration_complete`=7 — на каждое зафиксированное успешное завершение приходится ~3.4 сорванных callback. Это не даёт вывести точный % отвала (JS-цель недомеряет базу сравнения), но сигнал «вход/колбэк ломается чаще, чем долетает до финиша» — реальный и большой.

Per-door UTM-атрибуция (`ym:s:UTMSource` × register/deploy reaches) технически работает (query проверен), но **двери ещё не отгружены** — за 30д есть только 1 строка с меткой `probe-0813f` (1 register-reach, 0 deploy). Механической связки визит→build→payment по UTM сейчас нет данных для расчёта — не гипотеза, а факт отсутствия трафика с меток. Помечаю per-door атрибуцию `unmeasured — no live doors yet`, инструмент готов, ждёт трафика.

## 5. Деньги — [live]

**succeeded-платежи за 30д, контекст:** 1 платёж, 990 ₽ (`payments`, id `37a8d276-...`).

**ГЛАВНАЯ НАХОДКА ПО ДЕНЬГАМ:** этот единственный succeeded-платёж — `org_id='dada'`, `customer_email=''`, `created_by_sub=''`, без ИНН/организации. `org_id='dada'` — внутренняя shared-org, на которую в `projects` навешано 37 проектов (включая `internal`, sandbox-проекты). Это внутренний/тестовый платёж, **не платёж внешнего клиента**. За 30 дней у Dada Cloud **нет ни одного идентифицируемого успешного платежа реального внешнего пользователя.**

При этом реальный спрос есть и он отклонён:
| payment | org_id | план | сумма | статус | email |
|---|---|---|---|---|---|
| e7b9a4d0 | artempro2021@bk.ru | startup | 990 ₽ | canceled | artempro2021@bk.ru |
| b0ff5c9c | artempro2021@bk.ru | business | 2900 ₽ | canceled | artempro2021@bk.ru |
| 1671e4a8 | artempro2021@bk.ru | business | 2900 ₽ | canceled | artempro2021@bk.ru |
| 25f07e96 | dada (внутр.) | business | 2900 ₽ | pending | — |
| + 3 внутренних canceled (dada/sandbox/alexkekiy — тестовые) |

**`artempro2021@bk.ru` — самый активный юзер окна (611 audit-строк, 30 билдов, 2 git-репо, реальные деплои каждые 1-3 дня весь месяц) — попытался заплатить ТРИ раза (14-15 августа, оба тарифа) и все три canceled. Ноль succeeded.** Это ровно профиль «сделал всё правильно, продукт не смог взять деньги» — самый ценный юзер месяца технически не смог стать платящим.

**Оговорка по методологии п.5:** запрошенный join «`payments.user_id != project.owner_id`» механически не считается — в `payments` нет колонки `project_id`/`user_id`, только `org_id` (текстовый, шаренный на много проектов у internal-org) и `created_by_sub` (в обеих строках выше пуст либо равен email, не uuid). Точная привязка платежа к конкретному проекту/овнеру в этой схеме отсутствует — это отдельный структурный гейт, не мой недосчёт. Помечаю строгий п.5-join как `unmeasured — payments has no project_id/user_id FK`, дал вместо него прямую находку (см. выше), которая для гейта «за что платить» важнее.

## Беклог-кандидаты

### Agent-chat listApps отдаёт проект/апп/логи, которых нет ни в одной таблице БД
Юзер `macmam@atomicmail.io` (2026-08-08) получил через агент-чат живой ответ `listApps`/`searchLogs` про проект `9008a656-6373-4ab8-b9cf-6dbf87544f70` / апп `9router` (фаза Ready, реальные логи). Ни `projects`, ни `environments`, ни `git_repos`, ни `builds`, ни `audit_events` не содержат ни одной строки по этому id — ни сейчас, ни когда-либо. Юзер не владеет ни одним проектом в БД вообще. Через 6 минут тот же тул для того же юзера/проекта уже отдаёт `apps:[]`. Нужно найти, из какого источника инструменты агент-чата (`listProjects`/`listApps`/`getAppState`/`searchLogs`) реально читают данные (похоже не Postgres, а k8s/кэш живого состояния) и почему провижининг новых юзеров может создавать живые ресурсы без канонической записи в `projects`/`environments` — это дыра и в аудите, и потенциально в изоляции тенантов.

### Payments не хранит project_id/user_id — платёж нельзя механически привязать к владельцу
`payments` держит только `org_id` (текст, шарится между десятками internal-проектов на `org_id='dada'`) и `created_by_sub`, который у реальных платежей либо пуст, либо равен email вместо uuid. Гейт «кто реально платит и за чей проект» посчитать SQL-джойном невозможно уже сейчас, и это будет мешать каждому следующему циклу денежного аудита. Нужна FK-колонка (`project_id` или `paid_by_user_id`) на таблице `payments`, проставляемая в момент создания платежа.

### Единственный самый активный новый юзер месяца 3 раза не смог заплатить
`artempro2021@bk.ru` — 611 audit-событий, 30 билдов, регулярные реальные деплои весь месяц — подал 3 заявки на оплату (990₽ startup, 2×2900₽ business) 14-15 августа, все `canceled`, ноль `succeeded`. Нужно поднять реальную причину отмены на стороне YooKassa/checkout (те же классы, что чинили раньше — `project_checkout_persists_pending_row_without_creating_payment`, `project_checkout_recorded_outcome_through_payers_own_context` — проверить, воспроизводится ли тот же паттерн для этого юзера) и связаться с ним напрямую, пока он ещё «тёплый» (последнее действие 8.5ч назад).

### Точка сдачи реальных юзеров — не техника, а `ViewApps`/`ViewProject` без действия
Из 12 «класс C» юзеров минимум 6-7 терминальны сразу после осмотра консоли (`ViewProject`/`ViewApps`), не дойдя до `CreateApp`/`UploadSourceArchive`. Это не баг и не отказ билда — люди просто не увидели повода нажать «создать». Кандидат на UX-эксперимент (onboarding CTA/empty-state на `ViewApps` без ни одного аппа), не инженерный фикс.

### `registration_complete` JS-цель недомеряет реальные регистрации в разы
7 reaches за 30д против 28 реальных строк в `users` за то же окно. Либо часть регистраций идёт мимо `frontend/app/callback/page.tsx:56` (другой SSO-путь, ad-block, бот-фарма без JS), либо localStorage-guard из `project_metrika_goal_reaches_are_events_not_people` ловит меньше, чем должен. Заводить отдельно от per-door атрибуции — это базовая точность воронки, а не вопрос дверей.
