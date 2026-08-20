---
id: 0347
status: open
prio: P3
stream: 6
title: 15 доменов ушли в failed/attach_timeout ОДНИМ пакетом 2026-08-04 15:47:00
created: 2026-08-04
sess: sess-0810m
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 15 доменов ушли в `failed/attach_timeout` ОДНИМ пакетом 2026-08-04 15:47:00 (sess-0810m, 2026-08-10, [live psql]) — секунда в секунду: `oxy`, `nextjs-fhvx20`, `myredis`, `dada-static-starter`, `a2ahub-landing`, `fan`, `fanbot`, `fastapi-rjcozy`, `echo-bot-demo`, `bot-nodocker`, `magic-mirror-cloud`, `m2-delwedge` и др. Это ОДНО событие платформы, а не 15 отказов — панель же показывает 15 строк, и владелец читает их как 15 проблем юзеров. Найти событие 08-04 15:47, схлопывать такие пачки в одну запись.
