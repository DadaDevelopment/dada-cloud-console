---
id: 0077
status: open
prio: P1
hypothesis: H02
title: НОЛЬ СРАБАТЫВАНИЙ SeedDatabaseDSN НЕОТЛИЧИМ ОТ «ФИЧА НЕДОСТИЖИМА»
created: 2026-08-14
sess: sess-0814h
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟠 НОЛЬ СРАБАТЫВАНИЙ `SeedDatabaseDSN` НЕОТЛИЧИМ ОТ «ФИЧА НЕДОСТИЖИМА» (sess-0814h, 2026-08-14, [live psql + code], hypothesis: H02, origin/main@1e9435ae) — `SeedDatabaseDSN` НЕ мёртвый код: живой в `backend/internal/api/db_dsn_delivery.go:157-187`, зовётся из `databases.go:476` (авто-путь, гейт `engine==""` И `appRef!=""` через `resolveSoleAppRef` — ровно ОДНО приложение в окружении) и `databases.go:825` (синхронно при reveal-credentials). За всю историю 0 строк при 12+ успешных `CreateServiceDatabase` ⇒ предусловие почти никогда не выполняется. ПРАВКА: на ветку «skip: precondition not met» в `databases.go:476` повесить лог/метрику с причиной (0 приложений / 2+ приложений), иначе «юзерам не нужно» и «фича физически недостижима» навсегда остаются одним и тем же нулём. Это ровно паттерн памяти `project_shipped_lever_can_be_structurally_unreachable`.
