---
id: 0492
status: open
prio: P1
stream: 2
title: Port-range validation rejects valid ports and silently reverts saved service port
created: 2026-09-04
---
Symptom [live feedback 2026-08-24, user kof97zip-adjacent cohort]: "Порт приложения не задается, пишет диапазон ошибкой, хотя порт входит в диапазон. + после того как задал порт сервиса и сохранил порт слетает обратно".
Two defects: (1) validation says out-of-range for a port that IS in range; (2) saved service port silently reverts after save.
Where: frontend service-port form + backend validation (search port range check in apps API). Hit by a NEW signup cohort user on the activation path (app -> service settings -> port) = funnel-path bug.
Next: repro both, fix validation bounds + revert, add regression test.
