---
id: 0342
status: open
prio: P0
stream: 6
title: Гейт покрытия аудита слеп ровно к форме macmam
created: 2026-08-10
sess: sess-0810m
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🔴 Гейт покрытия аудита слеп ровно к форме macmam (sess-0810m, 2026-08-10, [code+live psql]) — `backend/internal/api/audit_coverage.go`, `GetAuditCoverage` джойнит ТОЛЬКО по `operation_id`, и юзер с живой чат/audit-активностью, но без единой операции, для гейта не существует. `macmam@atomicmail.io`: 0 projects, 3 SessionStart, 34 сообщения в чате, 41ч+ без реакции. Каскадное стирание `audit_events` при `DeleteProject` УЖЕ починено миграцией `110_audit_events_project_fk_set_null.sql` (`6d5c4e93`, `ON DELETE SET NULL`, проверено построчно) — но следующий такой случай мы всё равно не увидим. Правка: второй чек — недавняя chat/audit-активность + 0 текущих `projects` по `owner_id` + след бывшего проекта в `metadata->>'project_id'` осиротевших строк.
