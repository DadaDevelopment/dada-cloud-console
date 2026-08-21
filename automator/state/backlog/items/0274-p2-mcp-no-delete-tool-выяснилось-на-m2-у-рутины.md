---
id: 0274
status: open
prio: P3
stream: 2
title: P2-MCP-NO-DELETE-TOOL · Выяснилось на M2: у рутины НЕТ пути удалить свой же ресурс
section: 🎯 ПОТОК 2 — Deploy speed & reliability как продукт
---
- [ ] P2-MCP-NO-DELETE-TOOL · Выяснилось на M2: у рутины НЕТ пути удалить свой же ресурс — REST `DELETE .../apps/:name` даёт 404 (бирер `dada-routine-svc` видит только 5 проектов орга `dada`, скретч-орг `alexkekiy` вне его KC-групп), а в Dada-MCP тулы delete нет вовсе. Следствие: любой M2, который создаёт ресурс, обязан оставлять мусор ИЛИ лезть в psql руками. Чинить со стороны MCP (добавить deleteApp/deleteProject), НЕ расширением KC-скоупов сервис-аккаунта втихую.
