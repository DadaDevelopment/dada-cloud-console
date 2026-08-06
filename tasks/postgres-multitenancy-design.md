# Postgres multi-tenancy: пулы, квоты, наблюдаемость

Дизайн. Триггер — запрос клиента `artemmendeleev@gmail.com` поднять
`shared_buffers` до 2-4GB на своей БД. Разбор показал, что тюнить нечего:
у клиента нет своего постгреса, а общий инстанс не выдержит и текущей
нагрузки.

## Исходное состояние (2026-08-06, live)

| факт | значение |
|---|---|
| инстанс | `databases/postgresql-0`, bitnami postgresql 17.5 |
| ресурсы контейнера | limit 1Gi RAM / 500m CPU, request 228Mi / 100m |
| фактическое потребление | 694Mi RSS |
| том | `data-postgresql-0`, 26Gi longhorn-prod |
| баз на инстансе | 33 |
| control plane на нём же | `cloud-console`, `keycloak` (+ `nexus`, `jira`, `n8n`, `powerdns`, `mlflow`) |
| крупнейший арендатор | `odds-research` (клиентский), 15 GB = 68% тома |
| `rolconnlimit` | `-1` у всех 27 ролей `svc-*` |
| `datconnlimit` | `-1` у всех баз |
| `statement_timeout` | `0` |
| `idle_in_transaction_session_timeout` | `0` |
| `temp_file_limit` | не задан |
| `pg_stat_statements` | не установлен (`shared_preload_libraries = pgaudit`) |
| `log_min_duration_statement` | `-1` |
| композиций БД | одна, `servicedatabasev2-postgresql-k10` |
| ProviderConfig | один, `postgresql-prod` |
| биллинг БД | по числу штук (`billing_meter.go:79,127`), без размера и ресурсов |

Вывод: изоляции нет ни по одному измерению. Любой арендатор может занять все
200 коннектов, повесить бесконечный запрос или залить том — и уронить Keycloak,
то есть SSO всей платформы. Мы этого не увидим (нет `pg_stat_statements`) и не
тарифицируем (15 GB стоит столько же, сколько 10 MB).

## Принятые ограничения

- Выделенный под на клиента — отвергнуто, слишком дорого на арендатора.
- Вместо этого: **пулы инстансов**, рендерятся одним чартом; арендаторы
  раскладываются по пулам, платформа живёт в своём.
- Квоты — только декларативно, через композицию. Ручных `ALTER` в проде нет.
- Размер и потребление — жёсткие квоты с принуждением, а не уведомления.

## 1. Пулы

Один чарт `postgres-pools` рендерит N StatefulSet вместо сегодняшнего одного:

- `pool-platform` — control plane (`cloud-console`, `keycloak`) и внутренние
  сервисы. Реплика включена, ресурсы щедрые.
- `pool-shared-{a,b,...}` — арендаторы. Без реплики на нижних тирах.
- дополнительный пул под тяжёлого арендатора, когда он перерастает shared.
  Это по-прежнему пул, а не под-на-клиента: в него можно положить и соседей.

Каждый пул даёт свой `ProviderConfig` (`postgresql-pool-a`) со своим секретом.
`ServiceDatabaseV2` получает поля `spec.pool` и `spec.tier`; композиция выбирает
`providerConfigRef` по `spec.pool` вместо сегодняшнего захардкоженного
`.Values.providers.sql.providerConfigName`
(`crossplane-platform-api/chart/templates/compositions/servicedatabase-composition.yaml:40`,
`:64`, `:79`, `:95` — четыре места).

Размещение при создании БД выбирает бэкенд: тир задаёт класс пула, внутри
класса берётся наименее загруженный (по числу баз и сумме `pg_database_size_bytes`).
Пул фиксируется в `resource_snapshots` и в XR — миграция между пулами делается
отдельной операцией, не переразмещением на лету.

## 2. Тиры и квоты

Значения тиров живут в values чарта `crossplane-platform-api`, попадают в XR
при создании и оттуда в композицию. Ноль ручных ALTER — всё через
`postgresql.sql.crossplane.io/Role`, который уже поддерживает `connectionLimit`
и `configurationParameters` (проверено на CRD в кластере).

| тир | коннекты | размер | statement_timeout | idle_in_tx | temp_file_limit | work_mem |
|---|---|---|---|---|---|---|
| free | 10 | 1 GB | 15s | 30s | 512MB | default |
| starter | 20 | 5 GB | 30s | 60s | 2GB | 8MB |
| business | 50 | 25 GB | 120s | 300s | 8GB | 16MB |
| dedicated pool | по договору | по договору | настраиваемо | настраиваемо | настраиваемо | настраиваемо |

Фрагмент композиции:

```yaml
forProvider:
  connectionLimit: {{ $tier.maxConnections }}
  configurationParameters:
    - name: statement_timeout
      value: {{ $tier.statementTimeout }}
    - name: idle_in_transaction_session_timeout
      value: {{ $tier.idleInTxTimeout }}
    - name: temp_file_limit
      value: {{ $tier.tempFileLimit }}
    - name: work_mem
      value: {{ $tier.workMem }}
```

Бэкфилл существующих 27 ролей — тем же путём: проставить `spec.tier` в живые XR,
Crossplane доедет сам. Отдельного скрипта не нужно.

CPU и IO нативных лимитов в postgres не имеют. Квотируем их косвенно
(`connectionLimit` + `statement_timeout` + `temp_file_limit`) и детектим шумного
соседа по доле `pg_stat_database_active_time_seconds_total` в пуле. Это
приближение, а не cgroup — так и надо описывать в тарифе.

## 3. Размер как жёсткая квота

Postgres не умеет per-database disk quota, поэтому принуждение делает воркер
(рядом с существующими воркерами gitops-agent), источник правды —
`pg_database_size_bytes` из Prometheus, он уже используется бэкендом
(`backend/internal/api/databases.go:121`).

Ступени:

1. **80% квоты** — уведомление владельцу проекта плюс баннер в консоли.
2. **100% квоты** — `ALTER ROLE "svc-x" IN DATABASE "y" SET
   default_transaction_read_only = on`. База переходит в read-only: данные целы,
   чтение работает, запись отбивается. Обратимо одной командой.
3. **рост продолжается** (autovacuum, temp) — `ALTER ROLE ... CONNECTION LIMIT 0`
   как последний рубеж, чтобы не утащить том пула.

Снятие — автоматически, как только размер опустился ниже 90% квоты.
Состояние в новой таблице `db_quota_state` (db_id, tier, limit_bytes,
observed_bytes, state, changed_at), каждый переход в `audit_events`.

Read-only выбран сознательно: он обратим, не теряет данные и не роняет соседей,
в отличие от отключения БД или расширения тома по факту.

## 4. Наблюдаемость

Всё строится на уже собираемых метриках, новых экспортеров не требуется.

**Платформенный дэшборд `Postgres pools`** (Grafana, есть embed-gateway):
на пул — коннекты против `max_connections`, использование тома, cache hit ratio
(`blks_hit / (blks_hit + blks_read)`), deadlocks, `temp_bytes`; таблицы
топ-5 баз по размеру и по `active_time`.

**Карточка в консоли** на странице БД: размер против квоты, коннекты против
квоты, состояние (ok / warn / read-only), кнопка апгрейда тира.

**PrometheusRule** (в dada-cloud, `helm/dada-cloud-console/templates/prometheusrule.yaml`,
с `keep_firing_for` — см. инцидент с флапом алертов): коннекты пула > 80%,
том пула > 80%, cache hit < 0.9 пять минут, любая база в состоянии read-only,
лаг реплики pool-platform.

**Слепая зона:** `pg_stat_statements` и `log_min_duration_statement` включить
при ближайшем рестарте инстансов. Без них вопрос «какой запрос жжёт CPU»
неотвечаем — сегодня мы отвечаем на него догадками.

## Порядок работ

- [ ] 1. Чарт `postgres-pools`: `pool-platform` + `pool-shared-a`, свои
      ProviderConfig и секреты. Ресурсы пулов от 4Gi, а не 1Gi.
      Включить `pg_stat_statements` и `log_min_duration_statement = 1s`.
- [x] 2. XRD + композиция: `spec.tier` с квотами через `connectionLimit` и
      `configurationParameters` — argo-infra `a6b28000`, раскатано,
      проверено живьём: `with_conn_limit 0 | with_params 0 | total 27`
      (дефолт `unlimited` = байт-в-байт то же поведение).
      Остаётся `spec.pool` и выбор `providerConfigRef` по пулу — после шага 1.
- [x] 3. Бэкенд: тир из плана биллинга в payload → XR, перенос приложения
      тащит tier дословно, реконсилятор кладёт живой `tier` в снапшот и в
      ответ API — `969e90d1`. Остаётся выбор пула при создании БД.
- [x] 4. Воркер квоты размера: `db_quota_state`, ступени warn 0.8 /
      read-only 1.0 / frozen 1.25 / снятие 0.9, аудит, письма владельцу —
      `885d2464` (рычаг `spec.enforcement` + операция
      `SetDatabaseEnforcement` — `627f4c69`, argo-infra `6cd84dc9`).
      Разрыв 1.0/0.9 — против флапа; frozen достижим только из read-only.
      Живьём не прогонялось: kubectl недоступен (TLS handshake timeout).
- [ ] 5. Биллинг: БД считается тиром и гигабайтами, а не штукой.
- [ ] 6. Наблюдаемость: дэшборд пулов, карточка в консоли, PrometheusRule.
- [ ] 7. Миграции: control plane в `pool-platform`; `odds-research` (15 GB)
      в отдельный пул с окном и уведомлением владельца.

Шаги 1-2 снимают основной риск (арендатор роняет SSO). Шаг 4 закрывает
переполнение тома. Шаг 7 — разовая операция с даунтаймом, планируется отдельно.
