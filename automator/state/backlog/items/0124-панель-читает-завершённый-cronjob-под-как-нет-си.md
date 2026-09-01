---
id: 0124
status: open
prio: P2
title: ПАНЕЛЬ ЧИТАЕТ ЗАВЕРШЁННЫЙ CronJob-ПОД КАК «НЕТ СИГНАЛА»
created: 2026-08-13
sess: sess-0813a
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟡 ПАНЕЛЬ ЧИТАЕТ ЗАВЕРШЁННЫЙ CronJob-ПОД КАК «НЕТ СИГНАЛА» (sess-0813a, 2026-08-13, [live /admin/overview + kubectl]) — 14 ресурсов в `no_signal` (`dnszone-poller`, `serviceidentity-reconciler`, `jobs`, `keycloak-config`, `n8n`, `pg-router`, `kserve-*`) живьём здоровы: часть Running, часть — CronJob-поды в `Completed 0/1`, что для отработавшего рана НОРМА. Health-tracker не умеет читать здоровье CronJob-типа. Не инцидент, но это шум в панели, из-за которого настоящий `no_signal` теряется. Класс `platform-truth`.
