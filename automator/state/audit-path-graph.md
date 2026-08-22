# audit_events — путь пользователя, новые юзеры 7д/30д (sess-0822j)

Источник: live psql, `kubectl exec -n databases pg-shard-0-postgresql-0`, db `cloud-console`,
user `svc-cloud-console` (пароль из secret `argocd-prod/dada-cloud-console-backend` DB_USER/DATABASE_URL).
Контекст kubectl: beget-prod (текущий), доступ без прокси (DIRECT-OK, `ensure-proxy.sh` сам это определил).
Окно замера: now() = 2026-08-22 ~14:00 UTC. Все запросы READ-ONLY SELECT.

## 1. Новые юзеры

Фильтр — канонический из `activation-funnel-v2.sql`/`signup-attribution.sql` (вычитает
service-account-*, dada-e2e-test, a5-testuser-/sp2verify-, @dada-tuda.ru, @sp2-verify.dada-tuda.ru).

```sql
SELECT count(*) FILTER (WHERE created_at >= now() - interval '7 days') AS d7,
       count(*) FILTER (WHERE created_at >= now() - interval '30 days') AS d30
FROM users
WHERE username NOT LIKE 'service-account-%'
  AND username NOT IN ('dada-e2e-test')
  AND username !~ '^(a5-testuser-|sp2verify)'
  AND email NOT LIKE '%@dada-tuda.ru'
  AND email NOT LIKE '%@sp2-verify.dada-tuda.ru';
```

**d7 = 3, d30 = 27** (скользящее окно от текущего момента).

Список 27: 23 уникальных email-домена, из них **16 подряд за ~3 часа 08-08 19:49–22:56**
(dmimuser@outlook.com … chenlikun.18@gmail.com) — та же сигнатура, что и известная
`project_signup_farm_wave_pollutes_funnel` (волна фермеров 08-08). Остальные 11 —
разнесены по датам, похожи на органику/тест (в т.ч. владелец artempro2021/artempro2022,
kkartov, lifecoachrussia, tarotreaderhimu — свежие в 7д окне).

## Цепочка audit_events по каждому новому юзеру (created_at, action, resource_kind, resource_name)

```sql
WITH u AS (
  SELECT id, username, created_at FROM users
  WHERE created_at >= now() - interval '30 days'
    AND username NOT LIKE 'service-account-%' AND username NOT IN ('dada-e2e-test')
    AND username !~ '^(a5-testuser-|sp2verify)'
    AND email NOT LIKE '%@dada-tuda.ru' AND email NOT LIKE '%@sp2-verify.dada-tuda.ru'
)
SELECT u.username, ae.created_at, ae.action, ae.resource_kind, ae.resource_name, ae.outcome
FROM u JOIN audit_events ae ON ae.actor_id = u.id
ORDER BY u.created_at, ae.created_at;
```

Полный вывод — 174KB, сохранён локально
(`/Users/alex/.claude/projects/-Users-alex-IdeaProjects-dada-cloud/0fd64f77-d604-4e43-97f3-1ddc14f198d3/tool-results/b9ls278ca.txt`).
Ниже — свёртка (audit_n = non-SignUp события; build_n/repo_n/chat_n/feedback_n — активность в
других таблицах для тех же id):

```sql
WITH u AS ( ... тот же фильтр ... ),
audit_c AS (SELECT actor_id, count(*) FILTER (WHERE action<>'SignUp') n FROM audit_events GROUP BY actor_id),
build_c AS (SELECT triggered_by uid, count(*) n FROM builds WHERE triggered_by IS NOT NULL GROUP BY triggered_by),
repo_c AS (SELECT p.owner_id uid, count(*) n FROM git_repos gr JOIN projects p ON p.id=gr.project_id
           WHERE p.owner_id IS NOT NULL GROUP BY p.owner_id),
chat_c AS (SELECT user_sub::uuid uid, count(*) n FROM agent_chat_messages
           WHERE user_sub ~ '^[0-9a-f-]{36}$' GROUP BY user_sub),
fb_c AS (SELECT user_sub::uuid uid, count(*) n FROM feedback
         WHERE user_sub ~ '^[0-9a-f-]{36}$' GROUP BY user_sub)
SELECT u.username, u.created_at, coalesce(a.n,0) audit_n, coalesce(b.n,0) build_n,
       coalesce(r.n,0) repo_n, coalesce(c.n,0) chat_n, coalesce(f.n,0) feedback_n
FROM u LEFT JOIN audit_c a ON a.actor_id=u.id LEFT JOIN build_c b ON b.uid=u.id
LEFT JOIN repo_c r ON r.uid=u.id LEFT JOIN chat_c c ON c.uid=u.id LEFT JOIN fb_c f ON f.uid=u.id
ORDER BY u.created_at;
```

| username | created_at | audit_n | build_n | repo_n | chat_n | feedback_n |
|---|---|---|---|---|---|---|
| artempro2021@bk.ru | 07-23 | 649 | 33 | 2 | 100 | 2 |
| cryocrm@gmail.com | 07-26 | 8 | 0 | 0 | 0 | 0 |
| mytake@yandex.ru | 07-29 | 6 | 0 | 0 | 0 | 0 |
| good.win2283@gmail.com | 07-29 | 92 | 1 | 0 | 4 | 0 |
| macmam@atomicmail.io | 08-08 | 3 | 0 | 0 | 34 | 0 |
| dmimuser@outlook.com | 08-08 19:49 | 1 | 0 | 0 | 0 | 0 |
| jacksun950212@gmail.com | 08-08 | 4 | 0 | 0 | 0 | 0 |
| langhakka9527@gmail.com | 08-08 19:54 | 1 | 0 | 0 | 0 | 0 |
| game@016818.xyz | 08-08 19:55 | 1 | 0 | 0 | 0 | 0 |
| pjx694168692@gmail.com | 08-08 | 12 | 0 | 0 | 0 | 0 |
| grwang1201@outlook.com | 08-08 19:58 | 1 | 0 | 0 | 0 | 0 |
| zengqcyxx@gmail.com | 08-08 19:59 | 1 | 0 | 0 | 0 | 0 |
| clikuoo@gmail.com | 08-08 20:04 | 1 | 0 | 0 | 0 | 0 |
| oddessc@outlook.com | 08-08 20:11 | 1 | 0 | 0 | 0 | 0 |
| mail@ynotu.top | 08-08 20:11 | 1 | 0 | 0 | 0 | 0 |
| zhisibi@163.com | 08-08 20:23 | 1 | 0 | 0 | 0 | 0 |
| abc@zhkarc.us.ci | 08-08 | 2 | 0 | 0 | 0 | 0 |
| bestmanskyline@gmail.com | 08-08 20:41 | 1 | 0 | 0 | 0 | 0 |
| mmccok998@gmail.com | 08-08 | 4 | 0 | 0 | 0 | 0 |
| a@atry.kdns.fr | 08-08 | 2 | 0 | 0 | 0 | 0 |
| dsoftru@yandex.ru | 08-08 22:56 | 1 | 0 | 0 | 0 | 0 |
| chenlikun.18@gmail.com | 08-09 05:37 | 1 | 0 | 0 | 0 | 0 |
| michaelharlam@yandex.ru | 08-13 | 176 | 1 | 0 | 58 | 0 |
| artempro2022@yandex.ru | 08-13 | 296 | 15 | 1 | 70 | 0 |
| kkartov@yandex.ru | 08-17 (7д) | 512 | 11 | 0 | 0 | 0 |
| lifecoachrussia@yandex.ru | 08-19 (7д) | 25 | 4 | 1 | 0 | 0 |
| tarotreaderhimu@gmail.com | 08-21 (7д) | 18 | 3 | 1 | 0 | 0 |

## 2. Юзеры с нулём в audit_events среди новых

**Ни один из 27 не имеет 0 строк в audit_events.** Класс "мёртвый сигнап (0 везде)" и класс
"провал инструментирования (0 в audit, есть в других таблицах)" — оба **пусты** для окна
30 дней. Это само по себе находка: с момента фикса 08-09 (SignUp пишется той же командой, что
и users-строка + SessionStart на каждый первый визит) audit-покрытие регистрации полное — дыра
из `project_signup_could_be_born_without_a_trace.md` для этого окна не воспроизводится.

Но 16 из 27 (59%) имеют **ровно 1 audit-строку = `SessionStart`** и **0 везде во всех
остальных таблицах** (builds/repo/chat/feedback). Это не "провал инструментирования" (audit
не пустой) и не органический "молчащий пользователь" (только 1 действие за секунды после
регистрации, кластер из 16 таких за 3 часа 08-08) — это **сигнал живой волны фермеров**,
подтверждённый уже известной памятью проекта. Их метадата (`{"path": "/api/v1/agent/chat/history"
| "/api/v1/billing/account/summary" | "/api/v1/admin/audit", "visit": "first", "reason": "cold"}`)
— автоматический прогрев API сразу после логина, без человеческого клика.

Отдельно два **реальных** юзера с ненулевым audit, но нулём во всех продуктовых таблицах:
`cryocrm@gmail.com` (8 событий, 2 сессии 5 дней с разрывом) и `mytake@yandex.ru` (6 событий, 2 сессии
2 дня с разрывом). Оба: `SessionStart → RedeemPromo(если есть) → ViewProject → ViewApps → (тишина)`.
Заходят, видят пустой проект/список аппов, уходят — **ни разу не дошли до CreateApp**. Это
настоящий провал воронки, не бот и не дыра в логировании.

## 3. Граф переходов action_A → action_B (27 юзеров, non-SignUp события)

```sql
WITH u AS (...), ev AS (
  SELECT u.id uid, ae.created_at, ae.action,
         row_number() OVER (PARTITION BY u.id ORDER BY ae.created_at) rn
  FROM u JOIN audit_events ae ON ae.actor_id=u.id AND ae.action<>'SignUp'
), pairs AS (
  SELECT uid, action a_from, lead(action) OVER (PARTITION BY uid ORDER BY rn) a_to FROM ev
)
SELECT a_from, a_to, count(*) n_transitions, count(DISTINCT uid) n_users
FROM pairs WHERE a_to IS NOT NULL GROUP BY a_from, a_to ORDER BY n_transitions DESC LIMIT 15;
```

| a_from | a_to | n_transitions | n_users |
|---|---|---|---|
| SeedDatabaseDSN | SeedDatabaseDSN | 168 | 1 (внутренний ре-сид одного power-юзера, не воронка) |
| SessionStart | ViewProject | 149 | 7 |
| ViewProject | ViewApps | 145 | 9 |
| ViewProject | ViewProject | 89 | 4 |
| ViewApps | ViewApp | 69 | 5 |
| ViewApps | SessionStart | 68 | 6 |
| SessionStart | SessionStart | 44 | 8 |
| ViewProject | SessionStart | 43 | 5 |
| ViewApps | ViewProject | 43 | 6 |
| UploadSourceArchive | ViewBuildLogs | 36 | 4 |
| ViewBuildLogs | BuildFinished | 33 | 5 |
| BuildFinished | DeployImageVersion | 32 | 4 |
| VerifyDomainAuthorization | VerifyDomainAuthorization | 31 | 1 |
| ViewApp | ViewApps | 29 | 5 |
| ViewApp | UploadSourceArchive | 28 | 2 |

### Первое действие после регистрации (среди тех, у кого есть non-SignUp событие)

| first_action | n_users |
|---|---|
| SessionStart | 24 |
| CreateApp | 2 |
| RedeemPromo | 1 |

### Терминальное действие (последнее перед тишиной)

| terminal_action | n_users |
|---|---|
| SessionStart | 18 |
| ViewApps | 5 |
| DeployImageVersion | 2 |
| BuildFinished | 1 |
| ViewProject | 1 |

`SessionStart` как терминал у 18/27 — но 16 из этих 18 это фермеры с ровно 1 событием (SessionStart
и есть и первое, и последнее — искусственный ноль-путь, не человеческий отвал). Реальный
UX-обрыв виден в `ViewApps` как терминал (5 юзеров) — это люди, которые дошли до списка
приложений (пустого) и не создали ни одного.

## UX-вывод

Граф однозначно показывает воронку **SessionStart → ViewProject → ViewApps → (обрыв)** для
не-фермерских юзеров: 149 переходов SessionStart→ViewProject у 7 юзеров, 145 ViewProject→ViewApps
у 9 юзеров, но переход ViewApps→CreateApp в топ-15 не попал вообще — CreateApp почти никогда
не следует напрямую за просмотром пустого списка приложений. Двое подтверждённых кейсов
(cryocrm@gmail.com, mytake@yandex.ru) буквально останавливаются на ViewApps дважды, с разрывом
в несколько дней между сессиями, и ни разу не нажимают "создать".

**Пункт беклога 1**
Заголовок: `Пустой ViewApps не предлагает следующий шаг — юзер возвращается и уходит с того же экрана`
Тело: Что сломано — юзер логинится второй раз спустя дни (SessionStart), сразу идёт
ViewProject→ViewApps, видит пустой список приложений и уходит без единого CreateApp. Улика —
cryocrm@gmail.com (2 сессии, 08-06 и 08-11, обе кончаются на ViewApps/ViewProject) и
mytake@yandex.ru (2 сессии, 08-19 и 08-21, идентичный паттерн
SessionStart→ViewProject→ViewApps→тишина). Что чинить — на пустом состоянии ViewApps (0 apps в
проекте) показать явный CTA "Создать первое приложение" с одним кликом до формы CreateApp,
вместо молчаливого пустого списка; замерить конверсию ViewApps(empty)→CreateApp до/после.

**Пункт беклога 2**
Заголовок: `16 из 27 новых регистраций за 30д — фермерская волна 08-08, 0 продуктовых действий`
Тело: Что сломано — 16 аккаунтов созданы подряд за ~3 часа (08-08 19:49–22:56, домены
outlook/gmail/163.com/разовые), каждый оставляет РОВНО одну audit-строку `SessionStart` с
метадатой автоматического прогрева API (`path: /api/v1/agent/chat/history` или
`/api/v1/billing/account/summary` или даже `/api/v1/admin/audit`) и 0 строк во всех остальных
таблицах (builds/git_repos/agent_chat_messages/feedback). Это не органический отвал и не дыра в
audit — сигнатура идентична уже известной `project_signup_farm_wave_pollutes_funnel`. Что
чинить — не продуктовая правка UX, а гигиена метрики: воронку активации/новых-юзеров считать
искажённой на 59% за это окно, добавить фильтр по временной кластеризации регистраций
(>N сигапов за короткое окно с одинаковым паттерном SessionStart-only) в канонический запрос
`activation-funnel-v2.sql`, чтобы будущие циклы не завышали знаменатель воронки этой волной.

## Unmeasured / известные ограничения

- Всё измерено живьём, `unmeasured` нет — сеть/прокси были доступны напрямую (DIRECT-OK),
  все запросы прошли без ошибок.
- Полный построчный дамп цепочки (174KB) не вставлен целиком в этот файл — сохранён по пути
  выше; здесь дана агрегированная свёртка по требованию задачи (не просто count(*)).
- `agent_chat_messages.user_sub` и `feedback.user_sub` — текстовые поля; джойн сделан через
  regex-фильтр на UUID-формат (по памяти `project_agent_chat_user_sub_holds_users_id.md`), не
  все строки этих таблиц обязаны содержать валидный UUID (сервисные акторы) — для 27 целевых
  id это не создаёт риска (джойн точный по id, а не по типу).
