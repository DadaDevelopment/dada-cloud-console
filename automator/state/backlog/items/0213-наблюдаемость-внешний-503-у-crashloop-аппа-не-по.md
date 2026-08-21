---
id: 0213
status: open
prio: P1
title: НАБЛЮДАЕМОСТЬ: ВНЕШНИЙ 503 У CRASHLOOP-АППА НЕ ПОРОЖДАЕТ НИ ОДНОЙ СТРОКИ В app_url_alerts
sess: sess-0806n
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟠 НАБЛЮДАЕМОСТЬ: ВНЕШНИЙ 503 У CRASHLOOP-АППА НЕ ПОРОЖДАЕТ НИ ОДНОЙ СТРОКИ В `app_url_alerts` (sess-0806n, [live]) — оба аппа bruzas (`workassistantbot`, `tvkassistantbot`) отдают 503 снаружи, `app_health_alerts` их видит (письма ушли 05-08), а `app_url_alerts` для этих namespace = 0 строк. Два вотчера расходятся: контейнерный видит, URL-овый молчит. Понять, почему кандидат отсеивается, и покрыть.
