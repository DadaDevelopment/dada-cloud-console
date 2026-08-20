---
id: 0346
status: open
prio: P3
stream: 6
title: reels-tracker-d2aa30.dada-tuda.ru: три источника расходятся, права строка домена
created: 2026-08-10
sess: sess-0810m
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] `reels-tracker-d2aa30.dada-tuda.ru`: три источника расходятся, права строка домена (sess-0810m, 2026-08-10, [live psql+kubectl+probe-external]) — строка `failed`, а снапшот `app_phase=Ready`, deploy 1/1 в `internal-prod`, PublicApi CR Ready; хост при этом **DEAD 0/6 узлов, 404**. Зеркально к `dada-lending-server-e6cb0b` (строка `failed` 25 суток, хост ALIVE 6/6, 200) — там панель врёт в минус. Значит «домен failed» сегодня не означает ничего в обе стороны. Заземлять хост `probe-external.sh`, а не снапшотом.
