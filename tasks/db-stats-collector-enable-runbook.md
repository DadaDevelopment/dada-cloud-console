# Runbook — включить сборщик статистики баз в проде

Сборщик (`DB_SHARD_ADMIN_DSNS`) написан и лежит в `main`, но в проде молчит:
ключа в секрете бэкенда нет, без кредов сборщик выходит на старте. Пока он не
запущен, страница базы честно показывает «показатели ещё не собраны», а
критерий приёмки цели нельзя проверить на живых данных — правилам нужны окна
длиной от суток до недели.

Проверено 08-07: в `dada-cloud-console-backend` (ns `argocd-prod`) ключа
`DB_SHARD_ADMIN_DSNS` нет.

## Что именно даётся сборщику

Соединение к инстансу с правом читать `pg_stat_*` **во всех** логических базах
шарда. Сборщик выполняет только `SELECT` по системным представлениям; данные
таблиц он не читает и не может: тексты запросов берутся из
`pg_stat_statements` уже нормализованными, константы там заменены на
плейсхолдеры до того, как их увидит платформа.

Это единственный шаг, где платформа получает доступ шире, чем «своя база».
Поэтому он вынесен в отдельное действие владельца, а не сделан агентом.

## Вариант A (рекомендуемый) — отдельная роль только на чтение статистики

Роль `pg_monitor` даёт ровно то, что нужно сборщику, и ничего больше.
Выполняется на инстансе `postgresql-0` (ns `databases`) под `postgres`:

```sql
CREATE ROLE dada_stats LOGIN PASSWORD '<сгенерировать>';
GRANT pg_monitor TO dada_stats;
```

`pg_monitor` — встроенная роль, она уже включает `pg_read_all_stats`, то есть
видимость `pg_stat_statements` и `pg_stat_*` по всем базам. Дополнительно ни на
одну тенантскую базу права выдавать не нужно: сборщику хватает `CONNECT`,
который по умолчанию есть у `PUBLIC`.

## Вариант B — существующий суперпользователь

Быстрее, но даёт консоли полный доступ к данным арендаторов. Брать только если
вариант A почему-то не проходит.

## Прописать и перезапустить

```bash
kubectl --context 83.222.27.62:26443 -n argocd-prod patch secret dada-cloud-console-backend \
  --type merge -p "{\"stringData\":{\"DB_SHARD_ADMIN_DSNS\":\"shard-1=postgres://dada_stats:<пароль>@postgresql.databases.svc.cluster.local:5432/postgres?sslmode=disable\"}}"
kubectl --context 83.222.27.62:26443 -n argocd-prod rollout restart deploy/dada-cloud-console-backend
```

Формат значения — `имя_шарда=DSN`, пар через запятую. Шард `pg-shard-0`
добавляется второй парой, когда до него дойдёт очередь; шард без кредов просто
не собирается — это деградация до «нет данных», а не крэш.

## Проверка

```bash
kubectl --context 83.222.27.62:26443 -n argocd-prod logs deploy/dada-cloud-console-backend | grep db-stats
```

Ожидается `db-stats: collector started interval=5m shards=1`. Через 15 минут в
контрол-плейне должны появиться строки:

```sql
SELECT shard, count(DISTINCT datname), max(collected_at) FROM db_stat_databases GROUP BY 1;
```

Дальше `/admin/db-shards` перестаёт показывать «замеров ещё нет», а на странице
базы владельца появляются карточки таблиц и запросы. Правила с недельным окном
(`unused_index`, `stale_stats`) начнут срабатывать через неделю непрерывного
сбора — раньше они молчат намеренно.

## Откат

```bash
kubectl --context 83.222.27.62:26443 -n argocd-prod patch secret dada-cloud-console-backend \
  --type merge -p '{"stringData":{"DB_SHARD_ADMIN_DSNS":""}}'
kubectl --context 83.222.27.62:26443 -n argocd-prod rollout restart deploy/dada-cloud-console-backend
```

Пустое значение = сборщик выходит на старте. Уже собранные строки остаются;
`DROP ROLE dada_stats;` убирает и доступ.
