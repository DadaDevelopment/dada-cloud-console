---
id: 0335
status: open
prio: P3
stream: 6
title: P2-ENVIRONMENTS-LIMITRANGE-COLUMN-IS-DEAD: environments.limit_range пуст {} у 62 из 63 окружений
sess: sess-0810e
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] P2-ENVIRONMENTS-LIMITRANGE-COLUMN-IS-DEAD (sess-0810e): `environments.limit_range` пуст `{}` у 62 из 63 окружений — БД-копия потолка неймспейса мертва. Любой код, который поверит этой колонке вместо живого k8s, тихо занулит логику почти везде. Либо наполнять, либо сносить сущность. Сейчас никто из потребителей на неё не смотрит — проверить перед сносом.
