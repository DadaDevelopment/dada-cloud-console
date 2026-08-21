---
id: 0326
status: open
prio: P2
stream: 6
title: P2-RECOG-DB-DROPPED-FROM-BACKUPS · recog-db тихо выпала из набора scheduled-бэкапов после 07-31
created: 2026-07-31
sess: sess-0803c
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🟡 P2-RECOG-DB-DROPPED-FROM-BACKUPS (sess-0803c, побочно из E48) · `recog-db` тихо выпала из набора scheduled-бэкапов после 07-31 [live psql] — не разобрано, opt-out юзера это или поломка реконсайлера. Форма ровно та же, что starvation, который чинили в E46/E48; тихая потеря бэкапов у живой базы дороже, чем выглядит.
