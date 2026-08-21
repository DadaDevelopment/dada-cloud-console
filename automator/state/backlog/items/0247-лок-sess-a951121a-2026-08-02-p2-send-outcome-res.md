---
id: 0247
status: open
prio: P0
title: ЛОК sess-a951121a (2026-08-02) P2-SEND-OUTCOME-REST · КОД В ПРОДЕ ЕДЕТ (f9f601d), ждём live-M2
created: 2026-08-02
sess: sess-a951121a
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🔒 ЛОК sess-a951121a (2026-08-02) P2-SEND-OUTCOME-REST · КОД В ПРОДЕ ЕДЕТ (`f9f601d`), ждём live-M2. `recordNotifySend` (`backend/internal/api/notify_audit.go`) пишет строку `SendNotification` на каждый исход отправки; подключено к VolumeAlert, AutoscaleCeiling, AutofixReady, AutofixFailed, AppHealthAlert. Поправка к прежней формулировке: call-site'ов не шесть, а пять — `6ff7e3e` убрал письма об успешном резайзе (правило «автоскейл = молчаливая магия»), в автоскейлере остался только потолок. Адрес получателя в метаданных НЕТ (152-ФЗ), есть `recipient_source`. Тесты real-DB: `TestRecordNotifySend_*` (3/3 PASS).
