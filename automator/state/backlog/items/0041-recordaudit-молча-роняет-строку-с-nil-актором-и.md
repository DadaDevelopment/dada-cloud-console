---
id: 0041
status: open
prio: P2
hypothesis: platform-truth
title: recordAudit МОЛЧА РОНЯЕТ СТРОКУ С nil-АКТОРОМ, И ЧАСТОТА НЕИЗВЕСТНА
created: 2026-08-15
sess: sess-0815q
section: Backlog (execution-bet)
---
- [ ] 🟡 `recordAudit` МОЛЧА РОНЯЕТ СТРОКУ С nil-АКТОРОМ, И ЧАСТОТА НЕИЗВЕСТНА (sess-0815q, 2026-08-15, [code `backend/internal/api/audit.go:410-415`], hypothesis: platform-truth, origin/main@50cd9b0b) — сам дроп осознан и задокументирован («строка без имени актора хуже отсутствия строки»), спорить с ним не надо. Спорно молчание: ветка не пишет ни лога, ни счётчика, поэтому вопрос «сколько действий живых юзеров мы теряем из-за неопознанного актора» не имеет ответа в принципе. Рядом уже есть готовый механизм — `logAuditWriteFailure` + `dada_audit_write_failures_total{action,reason}` из `bff73b02`; нужен ровно ещё один reason-код `nil_actor`. Пруф = ненулевой (или доказанно нулевой) счётчик за сутки после раскатки.
