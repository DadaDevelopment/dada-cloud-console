---
id: 0162
status: open
prio: P0
title: api-zerkalo-ru (проект Default) — НАСТОЯЩАЯ поломка в not_ready_other, не тёзка
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] `api-zerkalo-ru` (проект Default) — НАСТОЯЩАЯ поломка в `not_ready_other`, не тёзка: CR `PublicApi/api-zerkalo-ru` в кластере **не существует вовсе** (`kubectl get publicapi -A` пусто), снапшот заморожен на коллизии с 07-21. Остальные два (`n8n`, `svod`) — известная слепота реконсилера на CR-тёзок (E103), их фиксы `a0c9942d`/`ab5e88a7` принципиально не лечат: у одних нет лейблов, у `api-zerkalo-ru` нет самого CR.
