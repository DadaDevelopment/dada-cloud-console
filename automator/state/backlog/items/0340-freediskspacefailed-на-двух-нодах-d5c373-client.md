---
id: 0340
status: open
prio: P2
stream: 6
title: FreeDiskSpaceFailed на ДВУХ нодах — d5c373-client-f675c9-npkxg-xnwvp **x1567 повторов** (69% из 95.8 GiB, чистка образов не освобо
created: 2026-08-10
sess: sess-0810m
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🟡 `FreeDiskSpaceFailed` на ДВУХ нодах (sess-0810m, 2026-08-10, [live kubectl]) — `d5c373-client-f675c9-npkxg-xnwvp` **x1567 повторов** (69% из 95.8 GiB, чистка образов не освобождает достаточно) и `d5c373-client-ff81fb-p47b7-zh58h` (79% из 105.5 GiB). Первый замер этого цикла увидел только вторую ноду и 15 повторов — часовой буфер событий занизил картину в сто раз. `DiskPressure=False`, `Ready=True` — юзеров сейчас не блокирует, но это ровно предвестник `project_node_disk_full_killed_platform_postgres`. Смотреть до того, как станет P0.
