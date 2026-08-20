---
id: 0039
status: open
prio: P3
title: ПОПРАВКА К СОСЕДНЕМУ ПУНКТУ ПРО recordAudit
created: 2026-08-15
sess: sess-0815r
section: Backlog (execution-bet)
---
- [ ] 🔵 ПОПРАВКА К СОСЕДНЕМУ ПУНКТУ ПРО `recordAudit` (sess-0815r, 2026-08-15, [code]) — дроп строки с nil-актором в `backend/internal/api/audit.go:410-415` НАМЕРЕННЫЙ, это прямо написано в коммите `2207e1ad1` (08-01). Добавлять туда `log.Warn` не надо, это будет фикс несуществующего бага. Настоящий вопрос — почему действия, инициированные агент-чатом (deploy/create/GC), вообще не доходят до `recordAudit`/`recordSystemAudit`.
