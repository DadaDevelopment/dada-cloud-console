---
id: 0354
status: open
prio: P3
stream: 6
title: ОТКАЗ ЧАТА НА daily_cap/not_configured НЕ ОСТАВЛЯЕТ СЛЕДА
created: 2026-08-11
sess: sess-0811d
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🔵 ОТКАЗ ЧАТА НА `daily_cap`/`not_configured` НЕ ОСТАВЛЯЕТ СЛЕДА (sess-0811d, 2026-08-11, [code backend/internal/api/agent_chat.go:1286-1315]) — оба ранних выхода возвращаются ДО `agentChatInsertMessage` и `agentChatRecordUserMessageAudit`, поэтому юзер, которому ассистент отказал, для аудита не существует вовсе. Инструментирование самих сообщений при этом работает (проверено 1:1 на живом срезе 08-07), ложная гипотеза «чат не пишет аудит» СНЯТА — дыра именно в отказных ветках.
