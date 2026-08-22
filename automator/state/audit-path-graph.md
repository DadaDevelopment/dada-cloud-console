# audit_events разбор — sess-0822f, 2026-08-22 02:0x-02:1x UTC

Гейт: `kubectl exec -n databases pg-shard-0-postgresql-0` (DSN из secret
`argocd-prod/dada-cloud-console-backend#DATABASE_URL`, база `cloud-console`).
Все числа ниже — живой SQL (`select now()` = 2026-08-22 02:10:07 UTC, max
`audit_events.created_at` = 02:05:07.97 — свежак, 5 мин).

## 0. Главный факт цикла: свипер class-fix (398f791f) сработал ЖИВЬЁМ и доехал до письма

Прошлый цикл (sess-0822e) отгрузил `398f791f` без единого живого прогона —
«честный предел». Сейчас есть первая живая строка:

```
2026-08-22 02:03:50.984692  system@dada.local  BuildAutoRetried
  best-marriage-astrologer-in-guwahati
  {class_fix_id: "static-npm-template-20260821",
   previous_build_id: "0f00460c-...", previous_fail_reason: "dockerfile_build_failed"}
2026-08-22 02:05:07.05      BuildFinished  status=success  attempt=2
2026-08-22 02:05:07.09      CreateApp      (первый деплой аппа юзера)
2026-08-22 02:05:07.97      SendBuildNotification recipient_source=owner status(email)=success
```

Проверено ДВА раза разными источниками: (1) строка в `audit_events` и (2)
лог build-agent `deploy-notify: sent app=best-marriage-astrologer-in-guwahati
recipient_source=owner status=success` за 2026-08-22 02:05 — письмо реально
ушло, не просто записалось в БД. Это закрывает E187 практическим успехом:
ребро «класс закрыт → сборка юзера повторена» теперь СУЩЕСТВУЕТ в графе, не
только в коде.

Что ещё НЕ известно: вернулась ли tarotreaderhimu@gmail.com посмотреть на
результат. Её последняя строка audit — 08-21 14:09:36 (BuildFinished failed),
силентность на момент этого замера 12.0ч — рано подтверждать возврат, но
письмо ушло почти 8 часов ПОСЛЕ её ухода, то есть догнать сама сессия уже не
может — только email-канал.

## 1. Новые юзеры

30д: 30 (без изменений численно), 48ч: 0 новых signup (tarotreaderhimu
08-21 13:58 — уже не в 48-часовом окне, последняя регистрация окна).

Zero-audit среди 30д-когорты: **0** (перепроверено builds/agent_chat — не
нужно, каждый SignUp пишет строку, подтверждено прямым `NOT EXISTS`-запросом
по всем 30 юзерам).

### Новая находка: три «тихих» юзера когорты внезапно стали самыми активными

Прошлый разбор (sess-0822c/d/e) фокусировался на tarotreaderhimu/
lifecoachrussia/mytake как на «свежих». Полный пересчёт audit-строк на 30д
показал трёх юзеров с кратно большей активностью, которых прошлые циклы не
разбирали цепочками:

| email | audit-строк | последняя | силентность |
|---|---|---|---|
| michaelharlam@yandex.ru | 168 | 2026-08-21 23:53 | 2.3ч |
| artempro2022@yandex.ru | 239 | 2026-08-21 21:48 | 4.4ч |
| artempro2021@bk.ru | 623 | 2026-08-21 21:17 | 4.9ч |

Все трое живы (силентность <5ч), не терминальны. artempro2021 (создан
07-23, всё ещё в 30-дневном окне) — самый тяжёлый юзер платформы по объёму
audit: 201×ViewProject, 106×SessionStart, 34×DeployImageVersion, 5×DeleteApp
— полноценный рабочий цикл разработки, не разовая проба.

У всех троих есть собственные `BuildAutoRetried` строки за 08-19 (attempt
2-4, `previous_fail_reason: platform_error`) — это СТАРЫЙ ретрай-механизм
(до class-fix свипера, другой код-путь: `status: "queued"` вместо
`class_fix_id`), не путать со свежим class-fix событием п.0.

## 2. Кто не сделал ничего / провалы инструментирования

Zero-audit: 0 (см. выше, прямой `NOT EXISTS`).

Chat→audit разрыв (`AgentChatUserMessage`) за 7д: **13 vs 13 = 0%** —
четвёртый цикл подряд 0%. Кандидат №4 прошлого цикла (историчность
инцидента до 08-07-хука) подтверждается ещё раз.

## 3. Граф путей (30д, join по users, исключены `@dada-tuda.ru` и `system`)

### Первое действие после SignUp (без изменений)
`SessionStart` 19, `SignUp` 6, `CreateApp` 2, `AgentChatActionDeclined` 1,
`CreateServiceDatabase` 1, `RedeemPromo` 1.

### Топ переходов action_A → action_B (self-loop исключён, n=20)
| A | B | cnt | distinct users |
|---|---|---|---|
| SessionStart | ViewProject | 223 | 10 |
| ViewProject | ViewApps | 202 | 12 |
| ViewApps | ViewApp | 102 | 8 |
| ViewApps | SessionStart | 98 | 8 |
| BuildFinished | DeployImageVersion | 70 | 10 |
| ViewProject | SessionStart | 70 | 8 |
| DeployImageVersion | SessionStart | 58 | 5 |
| ViewApps | ViewProject | 54 | 8 |
| ViewApp | ViewApps | 43 | 7 |
| SetEnvVar | DeployImageVersion | 40 | 6 |
| ViewBuildLogs | BuildFinished | 35 | 6 |
| ViewProject | ViewApp | 34 | 5 |
| UploadSourceArchive | ViewBuildLogs | 32 | 4 |
| DeployImageVersion | SetEnvVar | 31 | 5 |
| TriggerBuild | DeployImageVersion | 28 | 3 |
| BuildFinished | CreateApp | 26 | 9 |
| SessionStart | ViewApps | 25 | 5 |
| ViewApp | UploadSourceArchive | 25 | 2 |
| ConnectGitRepo | TriggerBuild | 24 | 7 |
| ViewApp | SessionStart | 23 | 5 |

Здоровый путь (`ViewBuildLogs → BuildFinished`, `TriggerBuild →
DeployImageVersion`, `BuildFinished → CreateApp` на 9 разных юзерах) —
доминирующая форма, не редкость. Патология: `SetDatabaseTier` self-loop 664
в сыром (без-self-loop-фильтра) графе целиком принадлежит бёрсту system-
актора 08-19 11:31 (см. §5), не новый рост — перепроверено точным подсчётом
outcome.

### Терминальное действие — срез "внешние реальные, тишина >24ч" (человеки, не internal)
| актор | терминал | часов тишины | metadata | классификация |
|---|---|---|---|---|
| lifecoachrussia@yandex.ru | DeployImageVersion | 42.9 | `gulyaev-ai-core`, success | ещё внутри 48ч, не churn |
| kkartov@yandex.ru | ViewApps | 54.8 | `apps:0, empty:true` | **churn** (снёс всё сам) |
| good.win2283@gmail.com | ViewApps | 181.0 | `apps:1, healthy:1` | **churn** (ушёл со здорового экрана) |
| cryocrm@gmail.com | ViewProject | 257.0 | `{}` | **churn** (0 repo/0 build) |
| + 15 ботов волны 08-08/09 | SessionStart | 308-318 | известная волна, не считается |

**Состав churn не изменился четвёртый цикл подряд** — те же 3 актора
(kkartov, good.win2283, cryocrm). tarotreaderhimu (12.0ч), michaelharlam
(2.3ч), artempro2022/2021 (4-5ч) — все внутри окна, живы.

## 4. Ретрай-петли / системные джобы (30д)

`SetDatabaseTier`: **332 failure / 12 success / 344 pending, max created_at
= 2026-08-19 11:31:58 — без изменений с прошлого цикла, ноль новых строк за
3-е сутки подряд.** Диагноз подтверждён третий раз тем же числом (332/12/344
идентичны sess-0822c/d/e) — джоб остаётся мёртвым, не «тихо падающим». Без
регрессии причины (нужен код/конфиг gitops-agent, не только SQL) — открытый
инженерный пункт, см. беклог 0466.

kkartov-петли (SeedDatabaseDSN 172/169, VerifyDomainAuthorization 31,
RevealEnvVar 23, SetEnvVar 14) — без роста, актор ушёл 54.8ч назад.

Новый факт (не петля, разовый бёрст 08-19 23:00:03): 4 разных юзера
(michaelharlam, artempro2021, artempro2022, bruzas.85@mail.ru) получили
СТАРЫЙ (pre-classfix) `BuildAutoRetried` с одинаковым timestamp
`23:00:03.585748` и `previous_fail_reason: "platform_error"` — похоже на
единый батч-ретрай инфраструктурного сбоя, не на per-user петлю. Не разбирал
глубже (не в скоупе цикла), но стоит на заметке: если платформенный
`platform_error`-класс регулярно бьёт 4+ юзеров одним временны́м пятном, это
кандидат в отдельный аудит.

## 5. SetDatabaseTier — статус без изменений (мёртв 3 цикла подряд)

332/12/344, max 2026-08-19 11:31:58 — идентично прошлому циклу. См. §4.

## 6. TriggerAutofix (0457) — не проверялся отдельно в этом цикле (не в скоупе задания); см. §0 для смежного механизма (class-fix свипер), который сработал.

## 7. tarotreaderhimu@gmail.com / best-marriage-astrologer-in-guwahati (0457/398f791f) — ДВИНУЛОСЬ

Полная цепочка [live psql]: SignUp 13:58:01 → CreateProject → StartGit/
FinishGitAppInstall → ConnectGitRepo → 3× (TriggerBuild → BuildFinished
failed, `dockerfile_build_failed`) между 13:58 и 14:09 → тишина.

**08-22 02:03:50** (12ч спустя её ухода): `system@dada.local` пишет
`BuildAutoRetried` с `class_fix_id: "static-npm-template-20260821"`,
`previous_build_id` = ровно её последний failed build (`0f00460c-...`).
Новый билд `3c8ebe82` (в таблице `builds`: `status=success`,
`created→finished` за 76с) → `CreateApp` (первый деплой этого аппа,
`framework: static`, домен `best-marriage-astrologer-in-guwahati-08c2af.
dada-tuda.ru`) → `SendBuildNotification` → **подтверждено логом
build-agent: email реально отправлен**, не только записан в БД.

Статус: класс закрыт, сборка автоматически повторена и доехала до живого
аппа с доменом, юзеру ушло письмо. Возврата юзера в консоль пока нет (её
последняя audit-строка всё ещё 14:09:36, силентность 12.0ч на момент
замера) — рано делать вывод "вернулась/не вернулась", письмо ушло 8ч назад,
собственной сессии после этого не было.

## 8. Граф — новое ребро существует, но однонаправленное

Единственная известная строка `BuildAutoRetried` с `class_fix_id` в системе
— эта. Ребро `class_fix → build_retry_success` в графе путей теперь имеет
1 инстанс. Симметричного ребра `email_sent → user_return_session` НЕТ и не
может быть измерено из audit_events (email opens не инструментированы) —
единственный способ узнать, сработало ли письмо, это будущая audit-строка
от tarotreaderhimu либо её отсутствие после разумного окна (48-72ч с
момента письма, т.е. проверка следующим-через-один циклом).

## 9. UX-вывод / беклог — обязательный пункт

**Терминальная точка с наибольшим числом юзеров:** `ViewApps` (пустой или
здоровый список) — 2 из 3 подтверждённых churn-акторов (kkartov,
good.win2283) закончили именно на этом экране, четвёртый цикл подряд без
изменений состава.

**Новый, более конкретный UX-разрыв этого цикла:** свипер class-fix чинит
билд и деплоит апп ПОЛНОСТЬЮ МОЛЧА для консоли — единственный канал
уведомления юзера письмо (подтверждено логом). Если юзер вернётся в
консоль (а не откроет письмо), карточка аппа на странице
`frontend/app/(console)/projects/[projectId]/apps/[appName]/page.tsx`
(смотри блок статусов вокруг `page.tsx:105` — `TERMINAL`-набор статусов
и `page.tsx:581-597` — блок баннера `urlStatus === "failed"`/`"pending"`)
не содержит НИ ОДНОГО признака "этот билд был автоматически пересобран
после нашего фикса" — `grep -rln "class_fix|classFix|BuildAutoRetried"
frontend` даёт 0 совпадений. Юзер, зашедший в консоль после письма (или
без него), увидит просто новый успешный билд без объяснения, почему
предыдущие три упали, а этот — нет; для юзера, который уже решил "платформа
сломана" (см. `project_shipped_lever_can_be_structurally_unreachable.md`),
это не читается как "мы это починили", а как необъяснимая случайность.

**Предложение в беклог:** добавить в ответ `GET` аппа/билда (или в payload,
который рендерит `page.tsx`) поле `metadata.class_fix_id`
(уже пишется в `audit_events` бэкендом — `backend/internal/api/
build_classfix_sweeper.go`) и на фронте — баннер рядом с блоком `page.tsx:
581-597` вида "мы обнаружили и исправили причину прошлых ошибок сборки
(`{class_fix_id}`), новый билд собран автоматически". Один инстанс события
есть живьём (`3c8ebe82`) — можно верифицировать баннер на этом же аппе,
не дожидаясь следующего срабатывания свипера.

## 10. Сравнение с прошлым циклом (sess-0822e)

- Главное изменение: E187 из "честный предел, живого прогона нет" стал
  "первое живое срабатывание, доказано двумя источниками (audit_events +
  build-agent log)".
- Churn-состав (kkartov, good.win2283, cryocrm) — без изменений 4-й цикл.
- Chat→audit разрыв — 0% 4-й цикл подряд.
- SetDatabaseTier — мёртв без изменений (332/12/344, идентично).
- Новое: три недооценённых по прошлым циклам активных юзера
  (michaelharlam, artempro2021/2022) с 168-623 audit-строками каждый —
  прошлые циклы их не разбирали цепочками, хотя они не новее по created_at,
  просто были заслонены свежими signup-ами в фокусе разбора.
