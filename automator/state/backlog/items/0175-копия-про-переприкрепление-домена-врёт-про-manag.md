---
id: 0175
status: open
prio: P0
title: Копия про переприкрепление домена врёт про managed-строки
created: 2026-08-10
sess: sess-0810f
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] Копия про переприкрепление домена врёт про managed-строки (sess-0810f, 2026-08-10, [code]) — `frontend/lib/i18n/console/messages/domains.ts:119` советит переприкрепить домен, а кнопка Detach в `frontend/components/deploy/hostnames-manager.tsx:282` рисуется только при `!h.managed`. У суррогатного домена такого контрола нет вообще: инструкция ведёт в никуда.
