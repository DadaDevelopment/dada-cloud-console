---
id: 0248
status: open
prio: P0
title: ЛОК sess-a951121a (2026-08-02) P2-BUILD-NOTIFY-UNAUDITED · КОД В ПРОДЕ ЕДЕТ (f9f601d, тот же коммит), ждём live-M2
created: 2026-08-02
sess: sess-a951121a
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🔒 ЛОК sess-a951121a (2026-08-02) P2-BUILD-NOTIFY-UNAUDITED · КОД В ПРОДЕ ЕДЕТ (`f9f601d`, тот же коммит), ждём live-M2. `db.RecordBuildNotify` (`build-agent/internal/db/notify.go`) пишет `SendBuildNotification` у обоих вызовов `notifyResult`, плюс ветка `no_recipient`, которая раньше молчала совсем — а это ровно случай markov-buturskiy: у владельца нет адреса, значит про упавший билд он не узнает никогда, и это теперь видно строкой. Тесты real-DB: `TestRecordBuildNotify_*` (3/3 PASS, отдельная БД `ba_test` — харнесс build-agent дропает схему, на общей `console` не идёт).
