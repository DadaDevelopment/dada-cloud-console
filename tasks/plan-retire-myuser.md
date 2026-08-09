# Вывод легаси-роли `myuser` и БД `mydatabase` на проектные базы

## Что есть сейчас (live, 2026-08-09)

Роль `myuser` — дотенантная, общая, заведена руками до шардинга. Никакого
`ServiceDatabaseV2` за ней нет, поэтому переезд шардов её не видит.

| БД на shard-0 | владелец | размер | потребитель | активность за 3 суток |
|---|---|---|---|---|
| `mydatabase` | `myuser` | 121 MB | `profi-backend` (проект `fin-core`, ns `fin-core-prod`) | 85182 коммита, до 20 бэкендов |
| `keycloakdb` | `myuser` | 12 MB | не найден ни один DSN в кластере | 15600 коммитов = фон сборщика статистики, 0 бэкендов |
| `codexlb` | `postgres` | 62 MB | `secret/codex-lb` в `argocd-beget` и `default`; воркло `codex-lb-workload` = 0 реплик | 15808, 0 бэкендов |
| `console-test` | `postgres` | 11 MB | не найден | 15783, 0 бэкендов |

`cm/postgresql-init-scripts` (ns `databases`) создаёт схемы `AUTHORIZATION myuser`
на shard-1 — тоже наследие.

Схемы внутри `mydatabase`: `profi` (38 таблиц, 96 MB, 231017 строк),
`fin_data_config` (14), `demo_fincore_6fd8b84a`, `demo_kompaniya`, `test_banka`,
`dada`, `profi_2` (по 31), `users` (3), плюс пустые `template`, `classification`,
`event`, `event_fabrique`, `feedback`, `public`.

## Допущение по границе распила

Пилим **по проекту-потребителю**, не по тенант-схемам. Тенанты внутри
`fin-core` — это схемы по замыслу приложения (`MAIN_DB_SCHEMA=fin_data_config`,
`app/utils/schema_guard.py` создаёт схему на тенанта). Разнос схем по отдельным
БД = переписывание приложения, а не наведение порядка в платформе.

Итог: одна проектная база `fin-core` под управлением `ServiceDatabaseV2`, роль
`svc-fin-core`, креды из секрета `fin-core-db-credentials`, бэкапы по расписанию.

## Шаги

- [x] 1. gitops: `service-databases-fin-core` (App + `ServiceDatabaseV2 fin-core`,
      ns `fin-core-prod`, shard-0, backup daily/7d, appRef `profi-backend`)
- [x] 2. синк подтверждён на самом шарде: `pg_roles` содержит `svc-fin-core`,
      `pg_database` — `fin-core`, секрет `profi-backend-db-credentials` на месте.
      Готовность CR как сигнал не годится: она поднимается раньше объектов
- [x] 3. пробный перелив: 210 таблиц, расхождений ноль
- [x] 4. окно: 210 таблиц / 233100 строк / 222 сиквенса (с `last_value`) /
      1331 индекс — совпало. Все объекты в `fin-core` за `svc-fin-core`,
      ни одного за `myuser`. Записей в окне не было: срез `mydatabase` после
      переключения совпал со срезом до перелива
- [x] 5. проверка: `/health` = `database: healthy`, `/auth/login/` = 401 JSON,
      `pg_stat_activity` показывает сессии `svc-fin-core` в `fin-core`
- [x] 6. `mydatabase` → `mydatabase--retired-2026-08-10` (122 MB держим как
      откат, владелец `svc-mydatabase`), приложение перепроверено после
      переименования
- [x] 7. `codexlb` разобран как проектная база: `ServiceDatabaseV2 codex-lb`
      (отдельный App `service-databases-codex-lb`), перелив сверен - 20 таблиц,
      8 сиквенсов с совпавшим хешем `last_value`, 59 индексов, `usage_history`
      64218, `request_logs` 4194, `reservations` 19406, `max(requested_at)`
      идентичен; 87 объектов за `svc-codex-lb`. Оригинал →
      `codexlb--retired-2026-08-10`, DSN в `values.yaml` переключён на роутер +
      `svc-codex-lb` + платформенный секрет. `keycloakdb--junk` и
      `codexlb--moved-to-shard-0` переливать некуда: первый - стоковый бутстрап
      (realm `master`, 1 юзер `keycloakadmin`, 6 стоковых клиентов, 0 событий)
      против живой `keycloak` с 98 юзерами; второй - побайтово тот же codexlb
- [x] 8. `myuser` вычищен из `cm/postgresql-init-scripts` (через
      `helm/databases/postgresql/values.yaml`), `DROP ROLE myuser` на shard-0
      прошёл. Секрет `codex-lb` больше никем не читается - оставлен как есть
- [x] 9. проверка: на обоих шардах ноль объектов за `myuser`; на shard-0 роли
      больше нет. Откатные копии целы: `codexlb--retired` 64218/19406,
      `mydatabase--retired` 38 таблиц в `profi`; `profi-backend` Running

## Не закрыто: `myuser` жив на shard-1 как логин экспортёра метрик

`DROP ROLE` на shard-1 нельзя: под `myuser` ходит metrics-сайдкар самого шарда
(`DATA_SOURCE_USER=myuser`, `127.0.0.1`, запрос по `pg_stat_archiver`). На
shard-0 сайдкар ходит под `postgres`, поэтому там роль снялась без последствий.

Объектов и привилегий за ролью на shard-1 нет - только идентичность экспортёра.
Чтобы снять: `auth.username` в `helm/databases/postgresql/values.yaml` увести в
пусто (чарт тогда отдаёт экспортёру `postgres`). Это env в pod spec, то есть
прокат StatefulSet прод-шарда, а на нём живая клиентская `odds-research`. Нужен
согласованный простой, отдельным окном.

Там же ждут окна `primary.initdb.user/password: myuser/mypassword` - латентные,
скрипт initdb на живом томе не выполняется.

## Что вскрылось по дороге

- Argo самолечит `replicas`: удержать приложение на нуле руками нельзя, окно
  надо закрывать коммитом в git, а не `kubectl scale`.
- Роутер искал роль не на том инстансе: `auth_dbname=postgres` не имя базы, а
  имя записи в той же таблице, и она попадала под `*` → дефолтный шард.
  `svc-fin-core` там нет, PgBouncer отвечал `FATAL: no such user`. Починено в
  консоли (`backend/internal/api/db_routes.go`): на каждый шард своя запись
  `dada_auth_<shard>`. Дыра не всплывала раньше, потому что все роли до распила
  на шарды до сих пор существуют на дефолтном шарде.
- Временный костыль на время выката: роль `svc-fin-core` продублирована на
  shard-1 тем же SCRAM-верификатором. Снят 10.08 после сборки #1028: в
  `routes.ini` живут `dada_auth_shard-0`/`dada_auth_shard-1`, роль на shard-1
  удалена (`pg_roles` там пуст по `svc-fin-core%`), обе реплики роутера
  переподключены, под пересоздан — `/health` = `database: healthy`,
  `/auth/login/` = 401 JSON, три сессии `fin-core|svc-fin-core`, внешний
  `https://profi.dada-tuda.ru/` = 200. Фикс роутера доказан на живом без клона.

- Проверка «объектов за ролью ноль» не значит, что роль снимается: `DROP ROLE`
  падает и на грантах. Считать надо не только `pg_class.relowner`, а гасить
  парой `REASSIGN OWNED BY ... TO postgres` + `DROP OWNED BY ...` в каждой базе,
  которую перечислил `DETAIL` ошибки.
- Приложение `apps/codex-lb` не рендерится в Argo с 09.08: воркло погашено в
  нуль коммитом `219f1b4f0`, а схема значений чарта требует `replicaCount >= 1`
  (`Failed to load target state ... minimum: got 0, want 1`). Ошибка одного
  источника обнуляет все три, поэтому `resources` того же App не применяются -
  включая `PublicApi codex-proxy`. Отсюда проектная база вынесена в отдельный
  App. Сама поломка не чинилась: воркло погашено намеренно, поднимать реплики
  без решения владельца нельзя.

## Инварианты

- Ни одного `DROP` до подтверждённой сверки строк.
- Оригинал `mydatabase` остаётся на месте как откат до пункта 6.
- Не создавать проектов; всё внутри существующего проекта `fin-core`.
