# Audit path graph (перезаписывается каждый цикл разбора)

## Окно этого разбора: 2026-09-20 (sess-0920a), скользящие 30 дней, 16 новых юзеров

## Главный вывод окна
Из 16 новых пользователей за 30 дней **5 (31%) закончили путь без единого живого приложения**
[live psql: `resource_snapshots kind='App' phase<>'Orphaned'`] — самая крупная течь окна.
Три из пяти (`saravananofficial13`, `ivakinavv23`, `y4ndex.danila`) уперлись в один класс:
`BuildFinished failure` с `fail_reason='framework_undetected'`/`dockerfile_build_failed`
[live psql `builds.fail_reason`] — сборщик не распознаёт репозиторий, и это последнее, что они
видят. Класс «пустая папка с html» чинили 09-10 (`86d64424`), но обычные node/python-репо без
манифеста всё ещё режутся тем же кодом. Ещё двое утекли до первого деплоя: `dada-tuda.ru1@buss.gq`
дважды кликнул «загрузить» без единого `UploadSourceArchive` в аудите (дыра инструментации),
`staffybot@mail.ru` утонул в 30-кратном retry-шторме `VerifyDomainAuthorization` (бэкофф
`dec39f68` от 09-11 пришёл ПОСЛЕ его инцидента 09-10).

## Цепочки новых юзеров (607 строк аудита) [live psql]
| email | signup (UTC) | событий | 6-е действие (развилка) | терминальное действие | live app? |
|---|---|---:|---|---|:---:|
| tarotreaderhimu@gmail.com | 08-21 13:58 | 19 | StartGitAppInstall | **BuildFinished / failure** | ✅ (с 3-й попытки) |
| dl592675334@gmail.com | 08-23 18:02 | 28 | StartGitAppInstall | UpdateAppStartCommand / success | ✅ |
| zqleaders@gmail.com | 08-25 02:58 | 19 | CreateAppServer *(failure×2)* | ViewApps / success | ✅ |
| m206rv159@yandex.ru | 08-26 04:44 | 65 | StartGitAppInstall | DeployImageVersion / success | ✅ (после 5× CreateServiceDatabase failure) |
| kof97zip@gmail.com | 08-28 02:48 | 15 | CreateApp | ViewApps / success | ✅ |
| messiajit4@gmail.com | 08-28 04:45 | 45 | StartGitAppInstall | ViewApp / success | ✅ |
| saravananofficial13@gmail.com | 09-02 05:27 | 16 | StartGitAppInstall | **BuildFinished / failure** | ❌ |
| wgck@sls.xx.kg | 09-03 01:36 | 81 | StartGitAppInstall | ViewApps / success | ✅ |
| ivakinavv23@yandex.ru | 09-03 08:00 | 18 | CreateProject (2-й) | ViewApp / success (после 2× TriggerAutofix failure) | ❌ |
| yzfy@sls.xx.kg | 09-05 09:07 | 56 | StartGitAppInstall | DeleteApp / success (удалил оба сам) | ❌ (самоочистка) |
| masaybasay@yandex.ru | 09-07 12:19 | 33 | StartGitAppInstall | ViewApp / success | ✅ |
| dada-tuda.ru1@buss.gq | 09-08 03:35 | **7** | SetAIRoutingMode | **CreateProject / success** (тупик) | ❌ |
| y4ndex.danila@yandex.ru | 09-09 12:03 | **9** | UploadSourceArchive | **BuildFinished / failure** | ❌ |
| staffybot@mail.ru | 09-10 08:18 | 44 | CreateServiceDatabase | ViewApps / success (после 30× VerifyDomainAuthorization failure) | ❌ |
| wow83168@gmail.com | 09-10 14:29 | 38 | StartGitAppInstall | ResolveAutofix / success | ✅ |
| freitorsk@yandex.ru | 09-12 09:52 | 114 | ResolveSolution | ViewApps / success | ✅ |

## Кто не сделал ничего
Нулей по всем таблицам в когорте **нет** [live psql: у всех 16 ≥7 строк audit_events].
Мёртвый до первого действия — только `dada-tuda.ru1@buss.gq` (7 событий, 2 клика
`empty-apps-cta-upload` в ux_events, 0 `UploadSourceArchive` в аудите). `yzfy` — не смерть,
а самоочистка (задеплоил и сам удалил). `agent_chat_messages`/`feedback` join по
`user_sub=keycloak_sub` дают 0 совпадений для всей когорты; чат за всю историю видел 11
уникальных `user_sub` из 70 юзеров — это инструментационный факт, не поведенческий.

## Граф переходов [live psql]
| from → to | count | distinct users |
|---|---:|---:|
| ViewProject → ViewApps | 46 | 16 |
| CreateProject → ViewProject | 18 | 16 |
| SignUp → SessionStart | 16 | 16 |
| SessionStart → CreateProject | 16 | 16 |
| VerifyDomainAuthorization → VerifyDomainAuthorization | 35 | **2** (retry-петля, не путь) |
| DeployImageVersion → DeployImageVersion | 19 | 8 |
| ViewBuildLogs → BuildFinished | 15 | 10 |
| BuildFinished → CreateApp | 12 | 9 |
| StartGitAppInstall → FinishGitAppInstall | 9 | 7 |
| ConnectGitRepo → TriggerBuild | 9 | 8 |

**Первое действие после регистрации.** SignUp → SessionStart (16/16) → CreateProject pending
(16/16) → ViewProject (16/16) → ViewApps (16/16) → развилка на rn6: CreateProject success 10/16,
StartGitAppInstall 4/16, CreateServiceDatabase 1/16, SetAIRoutingMode 1/16. Онбординг до 5-го
шага детерминирован; первая реальная развилка — шаг 6.

**Терминальное действие.** ViewApps/ViewApp success 8/16 (50%), BuildFinished failure 3,
DeployImageVersion success 1, UpdateAppStartCommand success 1, DeleteApp 1, CreateProject
success 1, ResolveAutofix success 1. Крупнейший кластер заканчивает на пассивном просмотре,
не на ошибке — причина остановки структурно невидима (см. долги инструментации).

## Вывод цикла, ушедший в код (fee1bb36)
Квотная стена 09-25 у 8 орг [live psql billing_accounts]. CTA всех ЧЕТЫРЁХ квотных поверхностей
(grace-banner, spend-widget, db-quota-panel, upgrade-dialog fallback) вели на `/pricing`, чьи
кнопки тарифов = `consoleHref('/login')`, а `/login` для залогиненного = `router.replace('/projects')`
[code]. Мёртвое кольцо: от предупреждения о стене не было пути к чекауту. Переведены на
`/projects/<id>/billing#billing-plans`; нет projectId — CTA скрывается (а не строится на undefined).
Плюс: баннер показывался только over-limit (1 орг из 8), теперь говорит и с at-limit группой
(7 из 8 сидят ровно на 1/1 и встретили бы стену голым 403).

## Долги инструментации (чего audit_events не может ответить)
1. Неуспешные логины/OAuth-callback не пишутся вообще — нужен `AuthCallbackFailed` с
   `outcome='failure'`, симметрично SignUp. 44% когорты словили `auth_callback_failed`
   [live psql ux_events], все вошли потом — самовосстанавливающийся race, но аудит слеп.
2. Пассивный уход (50% терминальных) неотличим от «вернусь позже» — нужен join с
   `ux_events.event_type IN ('visibility','nav_leave')` в регулярном path-graph.
3. Клик Upload → аудит-строка разрывается при клиентской ошибке/отмене: нужен
   `UploadSourceArchiveAttempted` в `backend/internal/api/uploadsource.go:60-66` ДО валидации.
4. Домен на проект без App/PublicApi не помечается — нужен `target_resource_exists` в metadata
   при `AddDomainAuthorization`.
5. `CreateAgent` не связан с визитом `/agents`: 5 визитов, 0 CreateAgent за 30 дней [live psql].

## Контроль-метрики (держать на нуле)
- `ux_events path LIKE '/projects/undefined%'` = 0 за окно (фикс 7f59ca7f жив).
- Любое ненулевое значение = P2-баг битой внутренней ссылки, источник искать по props.from.

*Все цифры — прямыми запросами psql к прод-БД через `kubectl exec -n databases postgresql-0`
в этом цикле [live psql]. Ничего не экстраполировано.*
