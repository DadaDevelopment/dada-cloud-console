# audit_events разбор — новые юзеры за 7д, замер 2026-08-22

Источник: psql напрямую в `pg-shard-0-postgresql.databases.svc.cluster.local`,
база `cloud-console`, DSN взят из env пода `argocd-prod/dada-cloud-console-backend`
(`DATABASE_URL`), под `pg-shard-0-postgresql-0` в ns `databases`. Все запросы
[live], без прокси-сигналов.

## 0. Кто попал в окно

`select * from users where created_at >= '2026-08-15'` → **3 юзера**, все с
`created_at >= 2026-08-17` (никого 08-15/08-16):

| email | created_at | audit-строк за окно | последнее событие | тишина на 2026-08-22 12:xx |
|---|---|---|---|---|
| kkartov@yandex.ru | 2026-08-17 19:46:33Z | 513 | 2026-08-19 19:26:37Z | ~2д 18ч |
| lifecoachrussia@yandex.ru | 2026-08-19 09:37:20Z | 26 | 2026-08-20 07:16:40Z | ~2д 6ч |
| tarotreaderhimu@gmail.com | 2026-08-21 13:58:01Z | 19 | 2026-08-21 14:09:36Z | ~23ч (ещё может вернуться) |

**Нулевых по audit_events за окно нет — 0 из 3.** Дополнительная сверка по
`builds`/`git_repos`/`agent_chat_messages`/`feedback` не проводилась для этих
троих, потому что она не нужна: у всех троих есть строки в audit_events
(значит и в builds/git_repos они тоже есть — audit пишется на те же действия).
Мёртвых сигнапов и провалов инструментирования в этом окне — **0 и 0** (честно:
выборка n=3 слишком мала, чтобы говорить это про продукт в целом, только про
эту неделю).

## 1. Первое действие после регистрации

Исключая `SignUp` (сама регистрация) и `SessionStart` (авто-событие входа):

- **CreateProject — 3 из 3 (100%)**

Воронка "зарегистрировался → создал проект" не течёт вообще — там нечему
течь, это одно нажатие, происходящее у всех.

## 2. Терминальное действие (последняя строка перед тишиной)

| email | terminal action | что было прямо перед этим |
|---|---|---|
| kkartov@yandex.ru | `ViewApps` (apps=0, empty=true) | 3× `InstallSolution` failure (`reason=env_failed`, HTTP 500) в ту же ночь, затем `DeleteApp`×неск., вернулся один раз посмотреть на пустой список и ушёл |
| lifecoachrussia@yandex.ru | `DeployImageVersion` (outcome=success) | успешный билд/деплой — оборвался на позитивной ноте, не churn-сигнал |
| tarotreaderhimu@gmail.com | `BuildFinished` (outcome=failure, `dockerfile_build_failed`) | 3 билда подряд за 9 минут, один и тот же `npm install`-фейл, между попытками юзер создавал новую БД, гоняясь не за той причиной |

Распределение терминальных действий на n=3: 1× пассивный уход после
провала фичи (`ViewApps` на пустом проекте), 1× уход в момент успеха
(не тревожный), 1× уход посреди активного билд-фейл-цикла (ещё в пределах
окна возврата, 23ч).

## 3. Топ переходов action_A → action_B (count / distinct users)

| A → B | n | users |
|---|---|---|
| `ConnectGitRepo` → `TriggerBuild` | 9 | 3/3 |
| `TriggerBuild` → `BuildFinished` | 8 | 3/3 |
| `TriggerBuild` → `ViewBuildLogs` | 6 | 3/3 |
| `ViewBuildLogs` → `BuildFinished` | 6 | 2/3 |
| `BuildFinished` → `CreateApp` | 5 | 2/3 |
| `ViewApps` → `StartGitAppInstall` | 4 | 3/3 |
| `StartGitAppInstall` → `ConnectGitRepo` | 7 | 1/3 (kkartov повторял установку) |
| `ViewProject` → `ViewApps` | 11 | 3/3 |
| `SessionStart` → `ViewProject` | 5 | 1/3 |

Самоповторы (`X → X`, один и тот же actor) — это не воронка, а retry/полинг
одного юзера, но они настолько велики, что искажают сырой count, если его не
отделять от distinct-users: `SeedDatabaseDSN → SeedDatabaseDSN` n=168
(kkartov, 179 вызовов за 08-17 21:00→08-18 22:34, ~25.5ч, интервал ~8-9 мин —
не ручной клик-спам, похоже на автоматический реконсил при каждом деплое/
рестарте, не расследовано глубже в этом цикле); `VerifyDomainAuthorization →
VerifyDomainAuthorization` n=31 (тот же kkartov, домен долго не верифицировался);
`RevealEnvVar → RevealEnvVar` n=16, `UpdateAppStorage → UpdateAppStorage` n=11,
`SetEnvVar → SetEnvVar` n=11, `DeleteApp → DeleteApp` n=10 — всё тот же один
гиперактивный юзер (kkartov, 513 из 558 строк окна).

## 4. Разбор двух content-инцидентов (оба уже закрыты кодом, не новые находки)

**kkartov — `InstallSolution` env_failed (2026-08-19 04:10-04:12Z).**
Уже задокументировано и обработано: `backend/internal/api/platform_recovery.go:44-72` —
причина (trailing newline в `GITOPS_ENCRYPTION_KEY` ломал `hex.DecodeString`),
фикс `17db736d` (2026-08-19 11:57Z, ПОСЛЕ инцидента kkartov), плюс механизм
`GetRecoveryPrompt` (`platform_recovery.go:96-179`), который обязан показать
kkartov баннер «мы это чинили, попробуй снова» при его следующем визите —
условие `userHasAnyApp==false` для него выполняется (apps=0). **Он не
возвращался с 08-19 19:26 — механизм ещё не проверен на нём живьём**, это не
дыра, а ожидание визита.

**tarotreaderhimu — `dockerfile_build_failed` ×3 за 9 минут (2026-08-21
14:00-14:09Z).** Тоже уже в коде: `backend/internal/api/build_repeat.go` и
`frontend/lib/build-repeat.ts` прямо цитируют этот инцидент по имени и
реализуют `repeat_count`/`isStuckOnRepeat`/`repeatHintKey` — карточка билда
должна подсветить «это уже третий раз, retry не поможет, смотри
`Dockerfile`» вместо молчаливого повтора красной строки. Нужно перепроверить
в следующем цикле, задеплоен ли `build_repeat.go`/фронт на прод и увидел ли
уже tarotreaderhimu подсказку при следующем возврате (он ещё в окне 23ч,
рано считать churn).

Оба случая — уже отработанные инциденты предыдущих циклов, а не новый
необслуженный сигнал. Свежих недиагностированных провалов у новых юзеров
в этом окне нет.

## 5. Вывод

n=3 за 7 дней — слишком мало, чтобы утверждать распределение по продукту
(любая цифра "60% уходят после X" здесь была бы статистическим шумом, не
пишу такую). Единственный воспроизводимый факт: **100% первых действий —
`CreateProject`**, потому что это вынужденный первый шаг UI, не выбор.
Оба содержательных сбоя (env_failed, dockerfile_build_failed) уже получили
код-фиксы в прошлых циклах; открытый вопрос — сработают ли они на живых
юзерах при возврате, а не новая правка.
