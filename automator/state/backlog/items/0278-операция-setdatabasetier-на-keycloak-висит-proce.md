---
id: 0278
status: open
prio: P1
title: ОПЕРАЦИЯ SetDatabaseTier НА keycloak ВИСИТ Processing 6.2ч — платформенная, не юзерская
created: 2026-08-14
sess: sess-0814
section: Открытые долги (не терять)
---
- [ ] 🟠 ОПЕРАЦИЯ `SetDatabaseTier` НА `keycloak` ВИСИТ `Processing` 6.2ч (sess-0814L, 2026-08-14T20:07Z, [live api `/api/v1/admin/overview` → `stuck_operations`], origin/main@712bd424) — платформенная, не юзерская. Панель показывает её честно, разбора причины не делал (правило одного яка: цикл занят TLS-DSN). Кандидат для следующего цикла: почему воркер тира БД не доводит операцию до терминального статуса и не срабатывает reclaim-путь мёртвой операции (память `project_operations_had_no_reclaim_path`).
  **ДОПОЛНЕНО тем же циклом [live logs, 20:33-20:36Z]:** причина видна целиком. Рестарт бэкенда (мой, ради доставки флага) дал немедленный тик `StartDBTierReconciler` — `db_tier_reconciler.go:51` гоняет `tick` сразу на старте, не дожидаясь часового тикера. Тик залил очередь операциями `SetDatabaseTier` на ПЛАТФОРМЕННЫЕ базы, и все они падают структурно, а не транзиентно: `no ServiceDatabaseV2 "nexus"/"powerdns"/"dadagent"/"n8n"/"jira-app"/"reels"/"user"/"telemost-bot"/"zerkalo"/"mydatabase" anywhere under clusters/.../apps`. Комментарий в `gitops-agent/internal/worker/db_values_locator.go:22` уже знает счёт: **17 из 21** таких флипов не находят манифест. `dbTierRetryAfter = 6h` не лечит, а нормирует вечный отказ в 4 операции в сутки на базу; при 17 нерешаемых это ~68 гарантированно провальных операций в день, плюс каждый рестарт бэкенда добавляет внеочередной залп. Побочный эффект, который я наблюдал сам: очередь `operations` разбирается `ORDER BY created_at` (`gitops-agent/internal/db/operations.go:100`), поэтому залп встал ПЕРЕД моими `DeleteApp`/`DeleteServiceDatabase` и задержал их. Чинить надо не таймаут ретрая, а резолвер пути: платформенные базы живут в `service-databases-*` манифестах, а не в `apps/<имя>/resources.values.yaml`, который угадывает локатор. Пруф работы = ноль `SetDatabaseTier` с ошибкой `no ServiceDatabaseV2 … anywhere under` за сутки.
