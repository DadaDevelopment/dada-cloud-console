---
id: 0094
status: open
prio: P1
title: CI В ОДНОЙ НОДЕ ОТ ГОЛОДАНИЯ — FailedScheduling pod/dada-cloud-console-agent-*
created: 2026-08-14
sess: sess-0814d
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟠 CI В ОДНОЙ НОДЕ ОТ ГОЛОДАНИЯ (sess-0814d, 2026-08-14, [live kubectl events]) — `FailedScheduling pod/dada-cloud-console-agent-*: 0/4 nodes available: 1 didn't match pod anti-affinity, 3 Insufficient memory`. `podAntiAffinity`, добавленный после инцидента с диском ноды platform-postgres, теперь сам стал связывающим ограничением: агенту сборки физически некуда встать. Пока не блокирует (билды проходят), но это ровно тот класс, что 08-13 останавливал доставку на 4 коммита.
