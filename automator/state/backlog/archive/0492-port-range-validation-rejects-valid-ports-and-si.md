---
id: 0492
status: closed
prio: P1
stream: 2
title: Port-range validation rejects valid ports and silently reverts saved service port
created: 2026-09-04
closed_at: 2026-09-12
closed_commit: 03bab269
closed_note: Разобран на две половины: range-ошибка уже была починена ранее (3613dc49, UpdateAppPort авто-снимает worker; фронт claims range только на invalid_port), а механизм "порт слетает" (full-object LWW в deployImageVersion) выделен в 0501. Живых данных по порту: 1 строка UpdateAppPort за всю историю платформы, 0 ux_events - не узкое место. Вместо этого отгружен реальный измеренный leak того же класса (portless upload = нет адреса молча): 03bab269.
---
Symptom [live feedback 2026-08-24, user kof97zip-adjacent cohort]: "Порт приложения не задается, пишет диапазон ошибкой, хотя порт входит в диапазон. + после того как задал порт сервиса и сохранил порт слетает обратно".
Two defects: (1) validation says out-of-range for a port that IS in range; (2) saved service port silently reverts after save.
Where: frontend service-port form + backend validation (search port range check in apps API). Hit by a NEW signup cohort user on the activation path (app -> service settings -> port) = funnel-path bug.
Next: repro both, fix validation bounds + revert, add regression test.
