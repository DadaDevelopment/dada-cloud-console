---
id: 0197
status: open
prio: P1
title: ЯКИ ПО ПУЛУ СОЕДИНЕНИЙ — (а) runWithAdvisoryLock (backend/internal/api/advisory_lock.go:61-84) держит пуловое соединение на всё вр
created: 2026-08-06
sess: sess-0807b
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟠 ЯКИ ПО ПУЛУ СОЕДИНЕНИЙ (sess-0807b, 2026-08-06, [code]) — (а) `runWithAdvisoryLock` (`backend/internal/api/advisory_lock.go:61-84`) держит пуловое соединение на всё время работы `fn(ctx)`, хотя соединение нужно только под сам advisory-lock; (б) `MaxConns` нигде не задаётся (`backend/internal/db/db.go:12-17`), размер пула = недокументированный дефолт pgxpool `max(4, NumCPU)` и молча меняется от ноды к ноде. Оба всплыли при разборе P0, ни один не тронут по правилу одного яка.
