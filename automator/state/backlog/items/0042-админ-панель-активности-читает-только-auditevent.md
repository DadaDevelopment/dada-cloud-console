---
id: 0042
status: open
prio: P1
hypothesis: platform-truth
title: АДМИН-ПАНЕЛЬ АКТИВНОСТИ ЧИТАЕТ ТОЛЬКО audit_events И ПОТОМУ ЗОВЁТ ЖИВОГО ЮЗЕРА МЁРТВЫМ
created: 2026-08-15
sess: sess-0815q
section: Backlog (execution-bet)
---
- [ ] 🟠 АДМИН-ПАНЕЛЬ АКТИВНОСТИ ЧИТАЕТ ТОЛЬКО `audit_events` И ПОТОМУ ЗОВЁТ ЖИВОГО ЮЗЕРА МЁРТВЫМ (sess-0815q, 2026-08-15, [live psql], hypothesis: platform-truth) — `macmam@atomicmail.io`: 3 строки в `audit_events` против 34 сообщений в `agent_chat_messages` и 223 строк в `ux_events`. То есть юзер работал в консоли весь сеанс, а срез активности показывает почти ноль. `ux_events` уже пишется и покрывает дыру ПОЛНОСТЬЮ — ждать доработки аудита, чтобы увидеть таких юзеров, не нужно. Действие: в срезе активности читать `ux_events` как fallback, когда у юзера пусто в `audit_events`, но есть чат/ux. Пруф = `macmam@atomicmail.io` перестал выглядеть мёртвым.
