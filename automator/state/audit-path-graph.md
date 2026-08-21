# audit_events разбор — sess-0821h, 2026-08-21

Гейт: `probe-prod-access.sh` ЗЕЛЁНЫЙ (k8s /readyz ok, psql exec ok, console 307). Все числа ниже измерены, не unmeasured.

DSN: `argocd-prod/dada-cloud-console-backend#DATABASE_URL`, exec через `postgresql-0` в ns `databases`.

## 1. Новые юзеры за окно (2026-08-20 → 2026-08-21)

Ровно **1** новый юзер: `tarotreaderhimu@gmail.com` (id `fa1cc1aa-2554-4d1f-ba72-7e6e2bd39ac4`, signup_source=awesome_webhosting, канал yandex), создан 2026-08-21 13:58:01.

Контекст 30 дней: 30 новых юзеров всего (см. §2 — большинство мёртвые).

### Цепочка нового юзера (полная, из audit_events, хронологически)

```
13:58:01.135  SignUp                User          tarotreaderhimu@gmail.com          success (user)
13:58:01.146  SessionStart          Session       tarotreaderhimu@gmail.com          success (user)
13:58:01.726  CreateProject         Project       tarotreaderhimu-gmail-com          pending (user)
13:58:03.003  ViewProject           Project       tarotreaderhimu-gmail-com          success (user)
13:58:03.310  ViewApps              AppList       9eca0ca6-...                       success (user)
13:58:15.503  StartGitAppInstall    git_installation  github                          pending (user)
13:58:21.780  FinishGitAppInstall   git_installation  github                          success (user)
13:58:48.208  ConnectGitRepo        GitRepo       best-marriage-astrologer-in-guwahati success (user)
13:58:48.504  TriggerBuild          Build         best-marriage-astrologer-in-guwahati success (user)   [build #1: 0ce47f4e]
13:58:48.797  ViewBuildLogs         Build         best-marriage-astrologer-in-guwahati success (user)
13:58:51.669  CreateProject         Project       tarotreaderhimu-gmail-com          success (system)
14:00:20.671  BuildFinished         Build         best-marriage-astrologer-in-guwahati failure (system)  [build #1 FAILED: npm install]
14:02:07.519  TriggerBuild          Build         best-marriage-astrologer-in-guwahati success (user)   [build #2: 207f9ae0]
14:03:36.823  BuildFinished         Build         best-marriage-astrologer-in-guwahati failure (system)  [build #2 FAILED: npm install]
14:07:47.259  CreateServiceDatabase ServiceDatabaseV2 db-8f66797a                     pending (user)   ← пробует чинить БАЗОЙ, не связано с ошибкой
14:07:57.624  CreateServiceDatabase ServiceDatabaseV2 db-8f66797a                     success (system)
14:08:05.524  TriggerBuild          Build         best-marriage-astrologer-in-guwahati success (user)   [build #3: 0f00460c]
14:09:07.273  SeedDatabaseDSN       ServiceDatabaseV2 db-8f66797a                     success (user)   ← ПОСЛЕДНЕЕ действие юзера
14:09:36.486  BuildFinished         Build         best-marriage-astrologer-in-guwahati failure (system) [build #3 FAILED: npm install]
--- тишина (>8ч на момент разбора) ---
```

Все 3 билда — идентичный `fail_reason=dockerfile_build_failed`, шаг `[build 5/6] RUN npm install`, разные timestamp-логи но одна и та же причина (репо `tarotwithhimu/Best-Marriage-Astrologer-in-Guwahati` — SEO-контентный сайт, вероятно шаблон с битым package-lock/зависимостью).

**Диагноз пути:** юзер трижды повторил TriggerBuild с одной и той же ошибкой npm install, между 2-й и 3-й попыткой создал и засеял сервисную базу данных — это НЕ имеет отношения к npm-ошибке, юзер гадал вслепую, куда копать. После 3-го провала — тишина. Это классический "ошибка не действенна → юзер чинит не то → уходит".

## 2. Кто не сделал ничего / провалы инструментирования

Проверка: SignUp **всегда** пишет ≥1 строку в audit_events (проверено на 30 юзерах за 30д — ни одного с audit_cnt=0). Значит "ноль в audit" здесь не встречается — но встречается "audit ≈ только SignUp, реальная активность в другом месте".

Таблица по всем 30 юзерам за 30 дней (audit_cnt / build_cnt / git_repo_cnt / chat_cnt(user_sub=users.id) / feedback_cnt(user_sub=keycloak_sub)):

**Мёртвые сигнапы (0 builds, 0 repos, 0 chat — утечка ДО первого продуктового действия), 20 из 30:**
cryocrm@gmail.com, mytake@yandex.ru, dmimuser@outlook.com, jacksun950212@gmail.com, langhakka9527@gmail.com, game@016818.xyz, pjx694168692@gmail.com, grwang1201@outlook.com, zengqcyxx@gmail.com, clikuoo@gmail.com, oddessc@outlook.com, mail@ynotu.top, zhisibi@163.com, abc@zhkarc.us.ci, bestmanskyline@gmail.com, mmccok998@gmail.com, a@atry.kdns.fr, dsoftru@yandex.ru, chenlikun.18@gmail.com, a.meshkov@dada-tuda.ru (внутренний).

18 из этих 20 зарегистрировались 2026-08-08 в окне 19:49–22:56 — это уже задокументированная ботоволна (`project_signup_farm_wave_pollutes_funnel.md`), не новая находка, но подтверждаю масштаб: **60% новых сигнапов за 30д — фермерский спам**, воронку это искажает если считать "сигнап→активация" без фильтра волны.

**Провал инструментирования (найдено, не тривиально):** юзер `17ffb57d-ae86-4ebb-b6f0-9678aea011c0@keycloak.local` (тестовый keycloak-аккаунт) имеет **1** строку в audit_events (только `AgentChatActionDeclined`), но **179** строк в `agent_chat_messages` (полноценный диалог с ассистентом 2026-08-03, включая реальную жалобу "за что списали 2900? нужен акт"). Похожий разрыв у `macmam@atomicmail.io` (audit=3, chat=34).

Копнул глубже: аудит-хук на `AgentChatUserMessage` **существует и подключён** (`backend/internal/api/agent_chat.go:1374`, `agentChatRecordUserMessageAudit`, добавлен намеренно — комментарий на 428–439 прямо говорит "assistant was a hole in the graph"). Но по факту:
- `agent_chat_messages` (role='user') с 2026-08-07 (дата вайринга) по 2026-08-20: **50** строк
- `audit_events(action='AgentChatUserMessage')` за тот же период: **37** строк
- **26% сообщений не долетают до audit** даже после того как хук был добавлен.

Это совпадает с уже задокументированной ловушкой `project_audit_events_silently_drops_rows.md` (`recordAuditAsync` роняет строки под нагрузкой/очередью) — не новый баг, но свежее подтверждение цифрами, что дыра ещё живая и на chat-пути тоже.

## 3. Граф пути (60 дней, actor_type='user', 43 уникальных актора)

### (a) Первое ЗНАЧИМОЕ действие после SignUp (SignUp/SessionStart исключены)
| action | юзеров |
|---|---|
| ConnectGitRepo | 8 |
| CreateProject | 8 |
| CreateApp | 3 |
| CreateServiceDatabase | 1 |
| RedeemPromo | 1 |
| AgentChatActionDeclined | 1 |
| ViewProject | 1 |
| BuildFinished | 1 |
| CreateMonitoringApp | 1 |

### (b) Терминальное действие (последнее действие юзера, событие старше 24ч на момент замера — т.е. "юзер реально замолчал")
| action | outcome | юзеров |
|---|---|---|
| **ViewApps** | success | **5** |
| **TriggerBuild** | success | **4** |
| **CreateApp** | success | **3** |
| ViewProject | success | 1 |
| DeployImageVersion | success | 1 |
| CreateProject | failure | 1 |
| AgentChatActionDeclined | success | 1 |

16 из 43 акторов молчат >24ч на момент замера (остальные активны в последние сутки — не терминальны, рано делать вывод).

**Ключевой сигнал:** топ-2 терминальные точки — `ViewApps` (5) и `TriggerBuild` (4) = **56% "сдавшихся"**. Юзер либо смотрит на список аппов и уходит (пусто/непонятно что дальше), либо запускает билд и не возвращается проверить результат — билд потом падает системным `BuildFinished failure`, но это уже `actor_type=system`, юзер этого события никогда не видел (не залогинился снова). Это совпадает 1:1 с цепочкой нового юзера из §1 — TriggerBuild как терминальная user-точка, за которой следует failure, о котором юзер не узнаёт синхронно.

## 4. Деньги

Все строки `payments` (9 всего, вся история):

| id | org_id | status | amount | yk_payment_id | payment_method | payer_org_name/email | created_at |
|---|---|---|---|---|---|---|---|
| 37a8d276 | dada | **succeeded** | 990 | 31f6cafd... | — | (пусто) | 2026-07-25 |
| 272512f3 | dada | canceled | 990 | — | — | — | 2026-08-13 |
| e7b9a4d0 | artempro2021@bk.ru | canceled | 990 | — | — | artempro2021@bk.ru | 2026-08-14 |
| b0ff5c9c | artempro2021@bk.ru | canceled | 2900 | — | — | artempro2021@bk.ru | 2026-08-14 |
| 1671e4a8 | artempro2021@bk.ru | canceled | 2900 | — | — | artempro2021@bk.ru | 2026-08-15 |
| eb4c8e48 | dada | canceled | 990 | — | — | sandbox-test@dada-tuda.ru | 2026-08-18 |
| a295a6e6 | dada | canceled | 990 | 321670d8... | — | alexkekiy@dada-tuda.ru | 2026-08-18 |
| 6ad2e12d | dada | canceled | 990 | 321670da... | — | alexkekiy@dada-tuda.ru | 2026-08-18 |
| 25f07e96 | dada | **pending** | 2900 | **NULL** | **invoice** | ООО "ДАДА ДЕВЕЛОПМЕНТ" (payer_inn=7807402712, invoice=INV-2026-00002) | 2026-08-19 |

**Реальный внешний платящий клиент: НЕТ.** Единственный `succeeded` (37a8d276, 990₽, 2026-07-25) — org_id='dada', пустой customer_email, пустой payer_inn — по всем признакам внутренний/тестовый платёж владельца, не сторонний клиент. Остальные внешние попытки (artempro2021@bk.ru — 3 штуки) все `canceled`.

**Новый `pending` без `yk_payment_id` (25f07e96, создан 2026-08-19 21:01, ПОСЛЕ фикса 3d6379f9 от 2026-08-15 16:57):** проверил сигнатуру против закрытого бага — НЕ совпадает. Закрытый баг = `pending AND yk_payment_id IS NULL AND payer_inn IS NULL` (карточный чекаут без юрлица). Эта строка имеет `payment_method='invoice'`, `payer_inn` ЗАПОЛНЕН, `payer_org_name` = собственное юрлицо Dada Development — это счёт-инвойс-флоу (банковский перевод), где `yk_payment_id` пуст ПО ДИЗАЙНУ (не через ЮKassa). Регрессии закрытого бага нет. Но: pending висит уже 2 дня без движения — если это тестовый прогон инвойс-флоу, он не завершён; если реальный — тоже стоит проверить, кто должен был подтвердить оплату.

## 5. UX-вывод / беклог

**Кандидат №1 (сильнейший, подтверждён и цепочкой §1, и графом §3b):** ошибка сборки `dockerfile_build_failed: npm install` не даёт юзеру достаточно контекста, чтобы понять причину — юзер трижды повторил идентичный неудачный билд и параллельно завёл БД (не связанную с ошибкой), т.е. диагностировал неправильно, потратил ~11 минут и ушёл. Это тот же паттерн, что топ терминальная точка `TriggerBuild`(4 юзера из 16 "сдавшихся" за 60д) — юзер жмёт билд и не возвращается, потому что не видит фейл синхронно/понятно.

Правка: в UI билд-лога/уведомления показывать хвост реального `npm error` (не только generic `dockerfile_build_failed`), и/или присылать push/email-уведомление о падении билда сразу, а не полагаться на то что юзер вернётся и посмотрит `ViewBuildLogs`. Смотреть код рендера ошибки билда в консоли: `frontend` компонент карточки билда/лога (искать по `fail_reason`/`dockerfile_build_failed` в `frontend/`) — не успел найти точный file:line в этом разборе, это следующий шаг для dada-engineer.

**Кандидат №2 (готовый code-level фикс):** `agent_chat.go:1374` — хук `agentChatRecordUserMessageAudit` подключён, но 26% chat-сообщений всё равно не долетают до `audit_events` (50 vs 37 за 08-07..08-20), подтверждая живую дыру `recordAuditAsync` (memory `project_audit_events_silently_drops_rows.md`). Беклог-пункт: сделать запись `AgentChatUserMessage` (и остальных `recordAuditAsync`-вызовов) синхронной или с retry/dead-letter вместо fire-and-forget — иначе граф пути в §3 систематически недооценивает chat-путь как способ ухода/жалобы (пример: юзер `keycloak.local` написал "за что списали 2900? нужен акт" в чат — это billing-жалоба, которая НЕ попала в audit вообще, только в chat-таблицу).

**Кандидат №3 (наблюдение, не блокер):** 60% новых сигнапов за 30д — ботоволна 08-08 (уже задокументирована), реальных новых людей за последний цикл — 1 (§1). Воронку/конверсию считать нужно за вычетом волны, иначе знаменатель врёт (уже известное правило `feedback_admin_numbers_business_meaning` + `project_signup_farm_wave_pollutes_funnel.md`).
