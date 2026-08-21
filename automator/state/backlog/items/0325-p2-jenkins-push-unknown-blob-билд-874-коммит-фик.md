---
id: 0325
status: open
prio: P2
stream: 6
title: P2-JENKINS-PUSH-UNKNOWN-BLOB · Билд 874 (коммит фикса 1efbba3) упал НЕ на тестах
sess: sess-0803c
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🟡 P2-JENKINS-PUSH-UNKNOWN-BLOB (sess-0803c, побочно, НЕ брал по правилу одного яка) · Билд `874` (коммит фикса `1efbba3`) упал НЕ на тестах — тесты прошли, стадия дошла до `docker push ghcr.io/.../dada-cloud-console-backend:1efbba3f` и получила `unknown blob` → `script returned exit code 1` [live jenkins log 874:1520-1536]. Та же семья, что память `project_nexus_large_layer_eof` (пуш крупного слоя рвётся, ошибка выглядит как порча дайджеста). Один флейк = один зря потерянный цикл доставки И ложный сигнал «main красный»; повторный пуш прошёл сам. Нужен ретрай `docker push` в `dadaBuildPipeline.groovy` с бэкоффом, иначе каждый такой флейк снова читается как регресс кода.
