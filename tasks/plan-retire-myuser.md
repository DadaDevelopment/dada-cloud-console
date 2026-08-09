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
- [ ] 7. архивные дампы `keycloakdb`, `codexlb`, `console-test` → дроп
- [ ] 8. `DROP ROLE myuser`, вычистить `myuser` из `cm/postgresql-init-scripts`
      и из `secret/codex-lb`
- [ ] 9. проверка: на обоих шардах нет ни объекта, ни привилегии за `myuser`

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

## Что осталось за `myuser` (live, 2026-08-10)

| шард | БД | объектов |
|---|---|---|
| shard-0 | `codexlb` | 119 |
| shard-0 | `keycloakdb--junk-2026-08-10` | 398 |
| shard-0 | `mydatabase--retired-2026-08-10` | 1777 + 13 схем |
| shard-1 | `codexlb--moved-to-shard-0` | 119 |

Пункты 7-9 упираются в `codexlb`: он в проекте `platform`, который read-only.
`DROP ROLE myuser` до его разбора невозможен — роль ещё владеет объектами.
Нужно явное добро владельца в диалоге.

## Инварианты

- Ни одного `DROP` до подтверждённой сверки строк.
- Оригинал `mydatabase` остаётся на месте как откат до пункта 6.
- Не создавать проектов; всё внутри существующего проекта `fin-core`.
