# Audit path graph (перезаписывается каждый цикл разбора)

## Окно этого разбора: 2026-09-25 (sess-0925a), новые с 2026-09-24 + скользящие 30 дней (с 2026-08-26)

## Главный вывод окна
С прошлого цикла (2026-09-24) появился **ровно один** новый пользователь: `artem4212@bk.ru`
(рег. 09-25 04:31:28) [live psql]. Это чистый, быстрый онбординг — от `SignUp` до `CreateApp/success`
за **4 минуты** (11 audit-строк) — но приложение `vishnevka` вошло в `CrashLoopBackOff` уже через
**72 секунды** после деплоя (`resource_snapshots.first_seen_at`=04:36:40, деплой в 04:35:28), с причиной
`ModuleNotFoundError: No module named 'aiogram'` [live psql, `app_health_alerts`]. Юзеру ушёл email
в 04:43, он его не открыл или не вернулся: последний `ux_events` в 05:08:31 — просто заглянул и закрыл
вкладку. Аудит-терминал юзера — `CreateApp/success`, но **живой терминал** (продуктовая правда) —
незамеченный краш-луп, который на момент сборки этого файла (06:04:38) **всё ещё активен**
[live psql, `resource_snapshots.phase='CrashLoop'` как последнего синка]. Ни одного мёртвого сигнапа
в новом окне нет — единственный новый юзер дошёл дальше многих (реальный деплой), но платформа не
удержала его после невидимого падения.

## Новые юзеры окна (с 2026-09-24) [live psql]
| email | signup (UTC) | событий audit | путь | терминал (audit) | живой статус сейчас |
|---|---|---:|---|---|---|
| `artem4212@bk.ru` | 09-25 04:31:28 | 11 | SignUp→SessionStart→CreateProject(pending→success)→ViewProject→ViewApps→UploadSourceArchive("")→SetUploadPort(worker)→ViewBuildLogs→BuildFinished(success,84с)→**CreateApp(success)** | CreateApp/success | ❌ CrashLoopBackOff, `ModuleNotFoundError: aiogram`, юзер не возвращался с 05:08 |

**Мёртвых сигнапов в новом окне: 0 из 1.** Единственный новый юзер прошёл дальше, чем «мёртвый
до первого действия» — задеплоил живое (на момент деплоя) приложение. Классифицировать его как
чистую активацию некорректно: приложение мертво прямо сейчас, просто это не видно в audit_events
(там нет terminal-действия «CrashLoop», только `CreateApp/success`) — см. §3 «Кто не сделал ничего»
для разбора этого расхождения.

## Кто не сделал ничего
Нулей по `audit_events` в 30-дневной когорте (15 юзеров) — **нет**, у всех ≥7 строк [live psql].
`agent_chat_messages`/`feedback` (join по `user_sub=keycloak_sub`) — **0 совпадений для всей когорты**
[live psql], инструментационный факт, не поведенческий (см. Долг №3).

`artem4212@bk.ru` — частный случай «ноль после последнего продуктового действия, но не мёртвый
сигнап»: 11 audit-строк, реальный деплой, **email-алерт доставлен и подтверждён** (`app_health_alerts.
last_send_ok=t`, `last_recipient='artem4212@bk.ru'`, отправлен в 04:43:05) — то есть инструментация
сработала (краш обнаружен и юзер уведомлён), но `ux_events` после 05:08:31 пуст: юзер не открывал
письмо или не вернулся в консоль. Это провал retention/re-engagement, не провал инструментации.

## Внутренние аккаунты (@dada-tuda.ru)
Ни одного нового `@dada-tuda.ru` в 30-дневном окне [live psql, явный запрос дал 0 строк].

## Цепочки 30-дневной когорты (с 2026-08-26; `zqleaders@gmail.com` от 08-25 выпал из окна, не показан) [live psql]
| email | signup (UTC) | событий | 7-й шаг (развилка) | терминал (audit) | активирован? |
|---|---|---:|---|---|:---:|
| m206rv159@yandex.ru | 08-26 04:44 | 65 | CreateProject / success | DeployImageVersion / success | ✅ |
| kof97zip@gmail.com | 08-28 02:48 | 15 | CreateApp / pending | ViewApps / success | ✅ |
| messiajit4@gmail.com | 08-28 04:45 | 45 | StartGitAppInstall / pending | ViewApp / success | ✅ |
| saravananofficial13@gmail.com | 09-02 05:27 | 16 | StartGitAppInstall / pending | BuildFinished / failure | ❌ |
| wgck@sls.xx.kg | 09-03 01:36 | 85 | UploadSourceArchive / success | ViewApp / success | ✅ |
| ivakinavv23@yandex.ru | 09-03 08:00 | 18 | CreateProject / pending | ViewApp / success | ❌ |
| yzfy@sls.xx.kg | 09-05 09:07 | 56 | UploadSourceArchive / success | DeleteApp / success (самоочистка) | ❌ |
| masaybasay@yandex.ru | 09-07 12:19 | 33 | CreateProject / success | ViewApp / success | ✅ |
| dada-tuda.ru1@buss.gq | 09-08 03:35 | 7 | CreateProject / success | CreateProject / success (тупик) | ❌ |
| y4ndex.danila@yandex.ru | 09-09 12:03 | 9 | UploadSourceArchive / success | BuildFinished / failure | ❌ |
| staffybot@mail.ru | 09-10 08:18 | 44 | CreateProject / success | ViewApps / success | ❌ |
| wow83168@gmail.com | 09-10 14:29 | 38 | CreateProject / success | ResolveAutofix / success | ✅ (CrashLoop) |
| freitorsk@yandex.ru | 09-12 09:52 | 114 | ResolveSolution / success | ViewApps / success | ✅ |
| drariaatashin@gmail.com | 09-22 08:23 | 80 | UploadSourceArchive / success | ViewProject / success | ✅ |
| **artem4212@bk.ru** | **09-25 04:31** | **11** | UploadSourceArchive / success | **CreateApp / success** | ✅ (CrashLoop) |

**Активация (≥1 приложение с `resource_snapshots.kind='App'` и `phase<>'Orphaned'`): 9 из 15 (60%)**
[live psql]. Из них **2 (artem4212, wow83168) сейчас в `CrashLoop`**, не `Ready` — «активирован» ≠
«приложение живёт»; это разные метрики, их нельзя схлопывать (см. Долг №5, новый в этом цикле).
**Не активировались: 6 из 15 (40%)** — `dada-tuda.ru1@buss.gq`, `ivakinavv23@yandex.ru`,
`saravananofficial13@gmail.com`, `staffybot@mail.ru`, `y4ndex.danila@yandex.ru`, `yzfy@sls.xx.kg`
(последний — самоочистка после успешного деплоя, не течь).

## Граф переходов, 30д когорта (с 2026-08-26) [live psql]
| from → to | count | distinct users |
|---|---:|---:|
| VerifyDomainAuthorization/failure → VerifyDomainAuthorization/failure | 86 | 3 |
| ViewProject/success → ViewApps/success | 46 | 15 |
| CreateProject/pending → ViewProject/success | 16 | 15 |
| SessionStart/success → CreateProject/pending | 15 | 15 |
| SignUp/success → SessionStart/success | 15 | 15 |
| BuildFinished/success → DeployImageVersion/success | 15 | 3 |
| ViewApps/success → ViewApp/success | 15 | 5 |
| SessionStart/success → ViewProject/success | 15 | 5 |
| BuildFinished/success → CreateApp/success | 12 | 9 |
| ViewApps/success → CreateProject/success | 11 | 10 |
| DeployImageVersion/pending → DeployImageVersion/success | 11 | 6 |
| BuildFinished/canceled → BuildFinished/canceled | 11 | 2 |
| DeployImageVersion/success → BuildFinished/success | 10 | 2 |
| ViewApps/success → ViewProject/success | 10 | 5 |
| ViewApps/success → SessionStart/success | 9 | 3 |
| BuildAutoRetried/success → BuildFinished/failure | 7 | 2 |
| UploadSourceArchive/success → ViewBuildLogs/success | 7 | 5 |
| BuildFinished/failure → BuildAutoRetried/success | 7 | 2 |
| SetEnvVar/success → DeployImageVersion/success | 7 | 3 |
| TriggerBuild/success → ViewBuildLogs/success | 7 | 5 |

**Первое действие после регистрации (шаг 7)**, 30д когорта, распределение:
`UploadSourceArchive/success` 5/15 (artem4212, drariaatashin, wgck, y4ndex.danila, yzfy),
`CreateProject/success|pending` 6/15 (dada-tuda.ru1, ivakinavv23, m206rv159, masaybasay, staffybot,
wow83168), `StartGitAppInstall/pending` 2/15 (messiajit4, saravananofficial13), `CreateApp/pending`
1/15 (kof97zip), `ResolveSolution/success` 1/15 (freitorsk). Онбординг до шага 6 детерминирован
(SignUp→SessionStart→CreateProject→ViewProject→ViewApps во всех 15 случаях), шаг 7 — первая
настоящая развилка «загрузить архив» vs «создать пустой проект/приложение» vs «поставить Git».

**Терминальное действие (audit), 30д когорта:**
| действие/статус | юзеров | кто |
|---|---:|---|
| ViewApp/success | 4 | masaybasay, ivakinavv23, messiajit4, wgck |
| ViewApps/success | 3 | kof97zip, staffybot, freitorsk |
| BuildFinished/failure | 2 | saravananofficial13, y4ndex.danila |
| DeployImageVersion/success | 1 | m206rv159 |
| ResolveAutofix/success | 1 | wow83168 |
| DeleteApp/success | 1 | yzfy (самоочистка) |
| CreateApp/success | 1 | **artem4212** — audit-терминал устарел, живой статус CrashLoop |
| CreateProject/success | 1 | dada-tuda.ru1 (тупик) |
| ViewProject/success | 1 | drariaatashin |

**Пассивный просмотр как последнее действие (ViewApp+ViewApps) = 7/15 (47%)**, крупнейший класс.
Для 2 из 15 (artem4212, wow83168) аудит-терминал вообще не отражает продуктовую правду: обоих
приложения сейчас в `CrashLoop`, но последнее записанное действие юзера — успешное (`CreateApp/
success`, `ResolveAutofix/success`) — см. Долг №5.

## Путь `artem4212@bk.ru` целиком [live psql, 11 audit-строк + resource_snapshots + app_health_alerts + ux_events]
`SignUp` 04:31:28 → `SessionStart` → `CreateProject`(pending→success, авто-дефолтный проект
`artem4212-bk-ru`) → `ViewProject` → `ViewApps`(apps:0, empty:true) → `UploadSourceArchive` "vishnevka"
04:34:03 (18799 байт, zip, **`framework: ""`** — детектор не нашёл ни один манифест) →
`SetUploadPort` 04:34:38 (юзер подтвердил "воркер", т.к. порт не определился — клик по ux-маркеру
`upload_no_port:worker`) → `ViewBuildLogs` → `BuildFinished` **success** 04:35:28 (84с сборки, без
`fail_reason`) → `CreateApp` **success** 04:35:28. **Живая правда после аудита**: `resource_snapshots`
фиксирует `App/vishnevka` в фазе `CrashLoop` уже в 04:36:40 (72 секунды после деплоя), причина
(`app_health_alerts.cause`) — `ModuleNotFoundError: No module named 'aiogram'`. Email-алерт ушёл
в 04:43:05, доставлен успешно (`last_send_ok=t`, получатель `artem4212@bk.ru`). Юзер вернулся в
приложение лишь один раз, в 05:08:29–05:08:31 (`session_start`→`pageview /callback`→`visibility
hidden`, **не дошёл даже до `/projects`**) и с тех пор — тишина. На момент составления файла
(06:04:38) приложение всё ещё `CrashLoop` (`last_seen_at`=06:04:05).

**Разбор**: `framework=""` при детекции архива по докстрингу `detect.go` должен приводить к отказу
сборки (`fail_reason=no_dockerfile`, см. `archive_redetect.go:58`, `detect_test.go:123`) — но у
`vishnevka` сборка отметилась **успешной**, без `fail_reason`, и создала работающий (по k8s) под,
который тут же начал падать на импорте `aiogram`. Это означает, что где-то на пути «пустой framework
+ подтверждённый worker» сборка проходит мимо ожидаемого `no_dockerfile`-отказа и порождает контейнер
без установленных зависимостей — новый, ещё не разобранный класс проблемы, отличный от `86d64424`
(тот чинил *статический html*, не голый python-воркер без манифеста). **Нельзя утверждать наверняка**,
был ли в архиве `requirements.txt` вообще (сырые байты архива в БД не хранятся, инспектировать нечем
кроме SQL) — это [HYPOTHESIS], а не подтверждённый факт; см. Долг №4 (новый).

## Путь `drariaatashin@gmail.com` после 2026-09-24 [live psql]
**Ноль audit-событий после 09-24** — юзер не возвращался. Его апекс `ariatala.ariaatashin.ir`
остаётся `verified` в `domain_authorizations` (статус `verified`, verified_at 09-22 08:41:18), но
**запись в `domain_hostnames` для него так и не появилась** (проверено: `select * from domain_hostnames
where authorization_id=<его id>` — 0 строк) [live psql]. Домен подтверждён на уровне DNS, но никогда
не превратился в реальный hostname/Ingress — застрявшая запись `CreatePublicApi` из инцидента 09-22
(409 `fqdn_taken`) так и не переприменилась, коммит `1dc89631` (09-23) закрыл вход в баг для будущих
юзеров, но не восстановил его собственный кейс. Однострочный итог: **юзер ушёл 09-22 и не
возвращался; его верифицированный домен `ariatala.ariaatashin.ir` всё ещё не обслуживает трафик**.

## Выводы UX, ушедшие в код

### 1. [live, новое в этом цикле] Успешная сборка с `framework=""` доходит до пользователя как рабочий деплой, хотя приложение обречено падать
`artem4212@bk.ru` — `BuildFinished/success` с пустым `archive_framework` в `builds` (id
`92153601-542b-4646-9dd2-750b59ee6f21`) и без `fail_reason`, хотя докстринг `detect.go:43` и тест
`TestDetectZipTelegramBot`/`archive_redetect.go:58` описывают именно этот случай (`Framework == ""`)
как повод для отказа `no_dockerfile`. Итог — контейнер стартовал и упал на `ModuleNotFoundError:
aiogram` через 72 секунды (`app_health_alerts`, `cause_kind='app_code'`).
**Бэклог-пункт (<=100 симв)**: `framework=""`+worker даёт success-сборку без deps — проверить путь
в архиве, `backend/internal/sourcedetect/detect.go` / `backend/internal/api/uploadsource.go:60-110`.

### 2. [live] Email-алерт о краше доставлен, но не приводит юзера обратно в консоль
`artem4212@bk.ru` получил рабочий email в 04:43 (`app_health_alerts.last_send_ok=t`), но
`ux_events` после 05:08:31 пуст — юзер либо не открыл письмо, либо открыл и не кликнул в консоль.
Ни один UX-маркер (`ux_events`) не покрывает клик по ссылке из email-алерта — невозможно отличить
«не увидел письмо» от «увидел и не заинтересовался». `backend/internal/api/app_health_watcher.go`
формирует письмо, но не проставляет UTM/трекинг-параметр на ссылку внутри.
**Бэклог-пункт (<=100 симв)**: добавить трекинг-параметр в ссылку email-алерта о краше, см. `app_health_watcher.go`.

### 3. [live, подтверждено повторно] Ручная кнопка "Проверить" домен всё ещё без кулдауна
Regressия из прошлого цикла не тронута в этом: `frontend/app/(console)/projects/[projectId]/domains/
page.tsx:558` (`handleVerify`, определён `page.tsx:288-301`) по-прежнему блокируется только на время
запроса (`disabled={busyId === auth.id}`), не учитывая `verifyDelayMs`/`VERIFY_MAX_ATTEMPTS`
(`frontend/lib/domain-verify-backoff.ts:13,16`), которые уже применены к авто-поллеру
(`page.tsx:262-286`). Не переисследовано заново в этом цикле (нет нового инцидента в новом окне),
но код не менялся — фиксирую как открытый, неисправленный пункт.
**Бэклог-пункт (<=100 симв)**: применить `verifyDelayMs`/`VERIFY_MAX_ATTEMPTS` к ручной кнопке `page.tsx:558`.

### 4. [live] Застрявший `drariaatashin` так и не восстановлен спустя 3 дня после фикса
`ariatala.ariaatashin.ir` verified с 09-22, `1dc89631` (09-23) закрыл баг для новых юзеров, но нет
джоба, который переприменяет `PublicApi`/создаёт `domain_hostnames` для уже застрявших владельцев
verified-апексов без записи в `domain_hostnames`. Подтверждено прямым запросом: 0 строк.
**Бэклог-пункт (<=100 симв)**: миграция/крон, восстанавливающий `domain_hostnames` для verified апексов без записи.

## Долги инструментации (чего audit_events не может ответить)
1. **Ручные клики vs автополлер неразличимы** (перенесено, не устранено): `VerifyDomainAuthorization`
   не несёт `metadata.trigger` ('manual'|'auto'). Нужное поле — `backend/internal/api/domains.go:269`.
2. **Пассивный уход неотличим от «вернусь позже»** (перенесено, не устранено, 47% терминалов —
   ViewApp/ViewApps): нужен join с `ux_events.event_type IN ('visibility','nav_leave')`.
3. **`agent_chat_messages`/`feedback` матчатся по `user_sub`**, не по `users.id` напрямую — 0
   совпадений для всей когорты может означать разрыв ключа `keycloak_sub`, а не молчание юзеров;
   не проверено в этом цикле, чист ли `keycloak_sub` у когорты.
4. **[новое] `builds`/`git_repos` не пишут, был ли реально выполнен шаг install зависимостей.**
   Для `artem4212` невозможно подтвердить SQL-запросом, отсутствовал ли `requirements.txt` в архиве
   или детектор его не нашёл — нужен структурированный лог шага сборки (`install: skipped|ok|failed`
   + причина), не просто финальный `status`. Без него «framework="" → success → CrashLoop» останется
   недоказуемым классом, а не воспроизводимым багом.
5. **[новое] Аудит-терминал ≠ продуктовый терминал.** `CreateApp/success` и `ResolveAutofix/success`
   как последние audit-записи (`artem4212`, `wow83168`) молчат о том, что оба приложения сейчас в
   `CrashLoop`. Путевой граф должен join'ить `resource_snapshots.phase` на момент отчёта к
   терминальному действию, иначе «терминал = успех» — оптический обман для 2 из 15 в этой когорте.
6. **Email-алерты не трекают открытие/клик** (см. UX-вывод №2) — нет способа отличить «не увидел» от
   «увидел и проигнорировал» на стороне продукта.

## Контроль-метрики (держать на нуле)
- `ux_events path LIKE '/projects/undefined%'` — снова не перепроверено в этом цикле, перенести дальше.

*Все цифры — прямыми запросами psql к прод-БД через `kubectl exec -n databases postgresql-0 --
psql "$DB_URL"`, где `DB_URL` — `argocd-prod/dada-cloud-console-backend` Secret `DATABASE_URL`
(base64-декодирован). Под `postgresql-0` в ns `databases`, предупреждение "Defaulted container
postgresql" — норма (второй контейнер — `metrics`). Сегодня (`now()` на момент цикла) — 2026-09-25
06:03–06:04 UTC. Прямой (без прокси) маршрут к k8s API работал весь цикл.*

### SQL, использованный в этом цикле (репрезентативные запросы)
```sql
-- Новые юзеры окна и 30д когорта (не-service/test/org)
SELECT id, email, created_at, signup_source, signup_channel
FROM users
WHERE created_at >= '2026-08-26'
  AND email NOT LIKE 'service-account-%' AND email NOT LIKE 'dada-e2e-test%'
  AND email NOT LIKE 'a5-testuser-%' AND email NOT LIKE 'sp2verify%'
  AND email NOT LIKE '%@dada-tuda.ru'
ORDER BY created_at;

-- Кто трогал продукт после 09-24 (проверка "не мёртвый ли весь новый список")
SELECT u.email, count(*) FROM audit_events ae JOIN users u ON u.id = ae.actor_id
WHERE ae.created_at >= '2026-09-24' GROUP BY u.email ORDER BY 2 DESC;

-- Терминальное действие на юзера, 30д когорта
SELECT DISTINCT ON (u.id) u.email, ae.action, ae.outcome, ae.created_at
FROM audit_events ae JOIN users u ON u.id = ae.actor_id
WHERE u.created_at >= '2026-08-26' AND <exclusion filters>
ORDER BY u.id, ae.created_at DESC;

-- Граф переходов
WITH cohort AS (SELECT id FROM users WHERE created_at >= '2026-08-26' AND <exclusion filters>),
ordered AS (
  SELECT ae.actor_id, ae.action, ae.outcome, ae.created_at,
         lead(ae.action) OVER (PARTITION BY ae.actor_id ORDER BY ae.created_at) next_action,
         lead(ae.outcome) OVER (PARTITION BY ae.actor_id ORDER BY ae.created_at) next_outcome
  FROM audit_events ae JOIN cohort c ON c.id = ae.actor_id
)
SELECT action||'/'||outcome, next_action||'/'||next_outcome, count(*), count(DISTINCT actor_id)
FROM ordered WHERE next_action IS NOT NULL GROUP BY 1,2 ORDER BY 3 DESC;

-- Активация: живое приложение (не Orphaned) через projects -> resource_snapshots
SELECT u.email, count(rs.id) live_apps
FROM users u JOIN projects p ON p.owner_id = u.id
LEFT JOIN resource_snapshots rs ON rs.project_id = p.id AND rs.kind='App' AND rs.phase <> 'Orphaned'
WHERE u.created_at >= '2026-08-26' AND <exclusion filters>
GROUP BY u.email ORDER BY u.email;

-- artem4212: полная цепочка + живой статус приложения
SELECT ae.created_at, ae.action, ae.outcome, ae.resource_name, ae.metadata
FROM audit_events ae JOIN users u ON u.id = ae.actor_id
WHERE u.email = 'artem4212@bk.ru' ORDER BY ae.created_at;

SELECT namespace, app_name, reason, detail, cause, cause_kind, first_detected_at, last_seen_at,
       last_sent_at, last_send_ok, last_recipient
FROM app_health_alerts WHERE app_name = 'vishnevka';

SELECT kind, name, phase, first_seen_at, last_synced_at
FROM resource_snapshots WHERE name = 'vishnevka';

SELECT occurred_at, event_type, path, target
FROM ux_events WHERE user_id = 'a6082056-5803-4d0a-b57a-7490749d0584' ORDER BY occurred_at;

-- drariaatashin: подтверждение, что домен не дошёл до domain_hostnames
SELECT * FROM domain_hostnames
WHERE authorization_id = 'c78dd37c-84b0-43b4-b7ea-e382bf9b80f7';  -- 0 строк
```
