---
id: 0378
status: open
prio: P2
stream: 2
title: Наш собственный домен a2a-hub.pro висит с failed-сертификатом в domain_issues
created: 2026-08-19
sess: sess-0820a
---
[live api `/api/v1/admin/overview`, 2026-08-19] Секция `domain_issues` несёт две записи. Одна —
`m2-delwedge-…`, юзерский scratch-проект проверки. Вторая — **`a2a-hub.pro`, проект «DADA Internal»,
то есть НАШ**, статус сертификата failed.

Не срочно (никакой живой юзер этим не заблокирован), но пункт остаётся в панели постоянным шумом:
пока он там висит, «в domain_issues что-то есть» перестаёт означать «надо смотреть». Либо чинить
серт, либо снести домен, если он мёртв (перед сносом — трейс читателей, память
`project_deleteapp_orphans_domain_row_under_live_app`).
