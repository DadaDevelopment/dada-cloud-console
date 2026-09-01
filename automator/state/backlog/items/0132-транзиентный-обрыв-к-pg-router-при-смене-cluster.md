---
id: 0132
status: open
prio: P2
title: ТРАНЗИЕНТНЫЙ ОБРЫВ К pg-router ПРИ СМЕНЕ ClusterIP
created: 2026-08-12
sess: sess-0812j
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟡 ТРАНЗИЕНТНЫЙ ОБРЫВ К `pg-router` ПРИ СМЕНЕ ClusterIP — ПРОВЕРИТЬ, НЕ ТЕРЯЮТ ЛИ СОЕДИНЕНИЕ ДРУГИЕ КЛИЕНТЫ (sess-0812j, 2026-08-12, [live kubectl + psql feedback]) — тикет artem'а `1b11ac69` (00:47Z) держит `No route to host` на `10.96.137.111:5432`; это ClusterIP СТАРОГО сервиса `postgresql.databases` (age 99д), а живой `pg-router.databases` = `10.96.139.238`. Под юзера пересоздан в 12:32Z и с тех пор Running/0 restarts — само-исцелилось. Вопрос открыт: откуда в приложении старый IP (кеш DNS/захардкоженный DSN/старый секрет) и не висят ли на нём другие `ServiceDatabaseV2`-клиенты. Смежное durable-знание: `project_pgbouncer_auth_dbname_resolved_on_default_shard`.
