---
id: 0372
status: open
prio: P1
stream: 2
title: Аудит чата пишется fire-and-forget: ходы диалога теряются, разговор юзера невидим
created: 2026-08-19
sess: sess-0819f
---
Найдено в разборе аудита sess-0819f 2026-08-19 [live psql].

`macmam@atomicmail.io` (`f71abeb0-c724-427f-9ecb-850a246ae4db`) вёл реальный 10-ходовый
диалог с AI-чатом 2026-08-08 20:42-20:54: задеплоил приложение, упёрся в
`preview upstream unavailable`, попросил помощи, не получил, ушёл. В `audit_events` —
НОЛЬ строк этого диалога, хотя `agentChatRecordUserMessageAudit`
(`backend/internal/api/agent_chat.go:440`, вызов на `agent_chat.go:1362`) живёт с 2026-08-07.

Причина: запись идёт через fire-and-forget `recordAuditAsync`
(`backend/internal/api/audit.go:562-568`) — горутина с 5с таймаутом. Тот же класс дефекта,
что уже чинили для SignUp коммитом `a447fcff` (2026-08-09,
`backend/internal/auth/provision.go:156-190`: сигнал пишется тем же statement, что и
сущность).

Почему важно: чат — единственное место, где юзер словами говорит, обо что он убился.
Терять эти ходы = слепнуть ровно там, где UX-вывод дешевле всего.

Фикс: писать audit чата синхронно, в одном statement с записью сообщения
(`agent_chat.go:263`), по образцу `a447fcff`.

Смежно (отдельным пунктом, НЕ здесь): нет action на ИСХОД диалога — предложен
`AgentChatSessionEnded` с `metadata.resolved`, иначе «помог ли чат» не отвечается
даже после этого фикса.
