# Квоты на размер БД + архив append-only данных в Parquet на S3

Статус: план принят 2026-08-14, реализация идёт по фазам.

## Зачем

В ночь на 2026-08-14 общий `postgresql-0` лёг на 9 часов. Причина в основе —
`odds-research` (free-план, тенант `artemmendeleev@gmail.com`) занимает 29 GB
из 32 GB тома и растёт ~2 GB в сутки. Ретенции у клиента нет, квота на размер
базы не применялась ни к одной базе в кластере.

## Что уже построено (не переписывать)

- `backend/internal/api/db_quota_watcher.go` — воркер квоты: раз в 30 минут
  читает `pg_database_size_bytes` из Prometheus, сравнивает с лимитом тира,
  пишет `db_quota_state`, ставит операцию `SetDatabaseEnforcement`, шлёт письмо,
  пишет аудит.
- `crossplane-platform-api` (репо argo-infra) — тиры с `storageLimit`:
  `free 1GB`, `starter 5GB`, `business 25GB`, плюс `unlimited`/`internal`.
- `gitops-agent/internal/worker/db_enforcement.go` — применяет enforcement в git.
- `databaseTierByPlan` в `backend/internal/api/databases.go` — маппинг плана на
  тир при создании базы.
- `billing_grace.go` + `notify.ComposeQuotaGraceReminder` — механика grace-окна
  для grandfathered-аккаунтов, образец для окна по базам.

## Чего не хватает (это и есть работа)

1. **Тир никогда не проставляется существующим базам.** Все 14 живых
   `ServiceDatabaseV2` стоят `tier=unlimited, enforcement=none`. Комментарий в
   `databases.go:41` ссылается на «tier reconciler», которого в коде нет.
2. **Нет архива в Parquet.** Слова `parquet` в репозитории нет вообще.
3. **Нет пути оплаты превышения.** Аддонов в биллинге нет; единственный путь —
   апгрейд плана.

## Решения (подтверждены владельцем)

- Grace для баз, которые уже превышают квоту в момент включения тира: **1 сутки**.
- Ступени `frozen` нет. Лестница: `none → warn(80%) → read-only(100%)`,
  освобождение на 90%. `read-only` — крайняя мера, а не наказание; короткое
  окно недоступности допустимо только на время самой операции архива/repack.
- Архив: **автоматически на free**, **по кнопке на платных планах**.
- Возврат места: **pg_repack в k8s Job** (по образцу `db_move_worker`), не
  переезд базы.

## Фаза 0 — включить то, что уже написано

- [x] `SetDatabaseTier` — операция в консоли (`models.SetDatabaseTierPayload`) +
      обработчик в gitops-agent (`db_tier.go` + case в `dbwatcher.go`), пишет
      `spec.tier` в values базы.
- [x] Tier reconciler (`backend/internal/api/db_tier_reconciler.go`, раз в час,
      advisory lock `000F`): сверяет тир снапшота с `databaseTierByPlan[plan]`
      организации, dedupe по незавершённой операции. Не трогает `internal` и
      проекты без `org_id`.
- [x] Grace-состояние: миграция `118_db_quota_grace.sql` (`grace_until`),
      чистая функция `applyDBQuotaGrace` (24 часа, выдаётся один раз на заход,
      снимается при возврате под 90%), письмо `ComposeDatabaseQuotaGrace`.
- [x] Убран `dbQuotaFreezeRatio` и переход в `frozen`. Записи `frozen` от старых
      сборок при первом же тике деградируют в `read-only`/`none`. Значение в
      XRD-enum остаётся.
- [x] Тесты: `TestDecideDBQuotaState_NeverFreezes`,
      `TestApplyDBQuotaGrace_GrantedOnceThenEnforces`,
      `TestApplyDBQuotaGrace_ClearedOnRelease`, `TestPatchDatabaseTier*`.

Не задеплоено. Первый тик реконсайлера проставит тиры всем живым базам, то есть
включит квоты в проде — выкатывать осознанно.

## Фаза 1 — архив в Parquet

- [x] Детектор append-only таблиц **уже существовал**: advisory
      `append_only_no_retention` (`db_advisories.go:258`, n_tup_del=0 за сутки +
      рост ≥1 ГБ/неделю), рендерится в консоли (`db-insights.tsx`). Не
      переписывать.
- [x] Планировщик отсечки (`backend/internal/api/db_archive_plan.go`,
      `GET .../tables/{table}/archive-plan`): выбирает колонку отсечки
      (`pickCutoffColumn` — индекс > NOT NULL > имя; created_at выигрывает у
      updated_at), гистограмма по месяцам (TABLESAMPLE 1% на таблицах от 1M
      строк, точный счёт ниже), при `?cutoff=YYYY-MM-DD` — точный счёт строк и
      оценка байт вместе с индексами. Живое чтение, ничего не пишет.
- [x] S3-приёмник: `S3Bucket` на проект (`dada-archive-<12 hex project id>`),
      заказывается через очередь операций (`CreateS3Bucket`, дедуп по
      NOT EXISTS), креды читаются `cloudtask.S3CredentialsResolver`; пока
      бакет провижинится, run ждёт в фазе `sink`
      (`db_archive_worker.go: sink/orderArchiveBucket`).
- [x] Archive worker + k8s Job по образцу `db_move_worker`: фазы
      `pending → sink → export → verify → delete → repack → done`
      (`migrations/119_db_archive_runs.sql`, `db_archive_worker.go`,
      `db_archive_jobs.go`, advisory lock `0x64616461_0010`).
      Экспорт — DuckDB (`ATTACH '' AS src (TYPE POSTGRES)` + `httpfs`,
      `COPY ... TO 's3://…' (FORMAT PARQUET, COMPRESSION ZSTD)`), проверка —
      отдельный Job: читает parquet обратно, сверяет точное число строк и
      `max(cutoff) < cutoff`, и только его exit-код открывает удаление.
      Удаление — батчами по ctid внутри бюджета 4 минуты, так что выкатка
      пода посреди удаления безопасна.
      Секреты не попадают в скрипты и командные строки: ключи S3 идут через
      credential chain DuckDB (`AWS_*`), пароль шарда — через `PG*` (libpq).
- [x] Guard по свободному месту: `repackHasHeadroom` требует 1.3× размера
      таблицы, свободное место берётся из
      `kubelet_volume_stats_available_bytes` по PVC шарда. Fail-closed: нет
      метрики, нет PVC, нет сэмпла — run падает с текстом для оператора,
      pg_repack не запускается.
- [x] Манифест архива в БД (`db_archive_runs.manifest`) + выдача клиенту:
      что уехало, за какой период, сколько строк, где лежит, как прочитать
      (`readDuckDB` / `readPandas`), и что бэкап по-прежнему всё содержит.
- [x] API: `POST/GET .../databases/{name}/archive-runs`
      (`db_archive_api.go`). POST проверяет таблицу вживую (колонка отсечки
      могла исчезнуть после превью), запрещает отсечку не в прошлом, 409 на
      второй параллельный run по той же таблице (частичный уникальный индекс).
- [x] Авто-архив на free (`db_archive_auto.go`): при ratio ≥ 1.0 или на старте
      grace берётся самая большая обычная таблица, отсечка выбирается как
      минимальная история, освобождающая место до 60% квоты; самый свежий
      месяц не трогается никогда. На платных тарифах — только кнопка (тот же
      POST).

## Фаза 2 — продуктовая обвязка

- [x] UI: `components/databases/db-quota-panel.tsx` — баннер (предупреждение
      >=80%, обратный отсчёт grace в часах, read-only), полоса квоты в HeroTile,
      кнопка «Архивировать» с выбором даты отсечения и точным предпросмотром
      (строки/объём считает `archive-plan` на каждую смену даты), история
      архивов с фазами, S3-ссылкой и однострочниками чтения (DuckDB/pandas),
      CTA «Поднять тариф» на `/pricing`.
- [x] `GetDatabaseInsights` отдаёт `quotaState`, `graceUntil`, `warnRatio` —
      без них баннеру нечем отличить «почти всё» от «уже read-only».
- [x] Письма: 80% и read-only были в фазе 0; добавлены «за 6 часов до конца
      grace» (`ComposeDatabaseQuotaGraceEnding`, одноразовая отправка через
      `grace_reminded_at`, миграция 120) и «архив выполнен»
      (`ComposeDatabaseArchiveDone`, разный текст для авто и ручного,
      S3 URI + как прочитать).
- [ ] Платное превышение (`+10 ГБ` SKU) — отдельная работа: аддонов в биллинге
      сейчас нет, нужен SKU и платёжный путь.

## Страховка

Бэкапы баз (`servicedatabasev2-postgresql-k10`, `@daily`) остаются как есть.
Архив не заменяет бэкап: удалённые строки лежат и в бэкапе, и в Parquet.
