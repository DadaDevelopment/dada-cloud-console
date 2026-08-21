---
id: 0176
status: open
prio: P0
title: 16 мёртвых хвостов в domain_hostnames — failed-строки без живого App-снапшота
created: 2026-08-10
sess: sess-0810f
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 16 мёртвых хвостов в `domain_hostnames` (sess-0810f, 2026-08-10, [live psql]) — failed-строки без живого App-снапшота: переименования (fan→fanvk, oxy→oxygen, nextjs-fhvx20→fonbet-value), ушедшие триалы, наш тестовый мусор (`m2-delwedge-6ccb0a-836ad9`, `excalidraw-probe-638f64`, `gl-anon-probe-54bfbf` — эти три наши, в agent-sandbox, убрать). Новый reattach-пасс их не трогает; они шумят в счётчиках поломок.
