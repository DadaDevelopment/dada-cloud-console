---
id: 0368
status: open
prio: P1
stream: 6
title: external_dsn НЕ ДОКАЗАН ЖИВЬЁМ, ПОТОМУ ЧТО ВНЕШНИЙ ДОСТУП НЕ ВЫСТАВЛЕН РЫЧАГОМ
created: 2026-08-14
sess: sess-0814f
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🟠 `external_dsn` НЕ ДОКАЗАН ЖИВЬЁМ, ПОТОМУ ЧТО ВНЕШНИЙ ДОСТУП НЕ ВЫСТАВЛЕН РЫЧАГОМ (sess-0814f, 2026-08-14, [live secret + code]) — connection-секрет managed-базы starter несёт только `endpoint/host/port/username/password`, ключей `external_endpoint`/`external_host` композиция не пишет, а тоггла внешнего доступа нет ни в API, ни в UI-пути создания. Значит поле `external_dsn` (`databases.go:825-833`) в принципе не материализуется штатным путём. Либо это мёртвый код, либо недостающая фича «дай мне подключиться к базе снаружи» — решить что именно, не оставлять в подвешенном виде.
