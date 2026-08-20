---
id: 0081
status: open
prio: P1
hypothesis: H02
title: resolveSoleAppRef НЕЛЬЗЯ ОТЛИЧИТЬ «НЕ НУЖЕН» ОТ «НЕДОСТИЖИМ»
created: 2026-08-14
sess: sess-0814i
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟠 `resolveSoleAppRef` НЕЛЬЗЯ ОТЛИЧИТЬ «НЕ НУЖЕН» ОТ «НЕДОСТИЖИМ» (sess-0814i, 2026-08-14, [live audit-проход + code], hypothesis: H02, origin/main@74f3b518) — `backend/internal/api/databases.go:476`: даже целевой самотест агента в `agent-sandbox` не смог триггернуть автосев `DATABASE_URL` (`app_ref_resolved: false`, `SeedDatabaseDSN` 0 строк за всю историю). Молчащая ветка «предусловие не выполнено» неотличима от «рычаг сломан» — ровно тот класс, на котором горел E137. ДЕЙСТВИЕ: лог+метрика на каждом раннем return с машинным reason, потом повторный самотест. Блокирует замер E140.
