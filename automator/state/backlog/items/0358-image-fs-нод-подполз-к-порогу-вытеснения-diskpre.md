---
id: 0358
status: open
prio: P2
stream: 6
title: image-fs нод подполз к порогу вытеснения — DiskPressure=False на всех 4 нодах СЕЙЧАС, но события FreeDiskSpaceFailed повторяются н
created: 2026-08-11
sess: sess-0811k
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🟡 image-fs нод подполз к порогу вытеснения (sess-0811k, 2026-08-11, [live kubectl]) — `DiskPressure=False` на всех 4 нодах СЕЙЧАС, но события `FreeDiskSpaceFailed` повторяются на всех четырёх (свежесть 2-8 мин), занятость image-fs 68% / 80% / 85-86% / **96%** (`d5c373-client-ff81fb-p47b7-zh58h`). 96% — это уже соседство с порогом; ровно так начинался `project_node_disk_full_killed_platform_postgres`. Не брал по правилу одного яка: клиентских поломок сейчас нет.
