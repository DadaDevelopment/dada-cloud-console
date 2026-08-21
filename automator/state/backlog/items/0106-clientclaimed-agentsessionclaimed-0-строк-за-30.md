---
id: 0106
status: open
prio: P2
title: client_claimed/agent_session_claimed = 0 строк за 30 дней — unmeasured, НЕ сломано
sess: sess-0813j
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟡 `client_claimed`/`agent_session_claimed` = 0 строк за 30 дней — `unmeasured`, НЕ сломано (sess-0813j). Синхронные write-хендлеры клейм несут, но живого write-события после выката не случилось; пассивные `ViewProject`/`SessionStart` не могут нести его НИКОГДА — `recordAuditAsync`/`recordSessionStartAsync` открывают `context.Background()` в горутине (`backend/internal/api/audit.go:527-533,623-629`). Перемерить после первого живого write-события, не раньше.
