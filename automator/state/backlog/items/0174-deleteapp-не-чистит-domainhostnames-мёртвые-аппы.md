---
id: 0174
status: open
prio: P0
title: DeleteApp не чистит domain_hostnames — мёртвые аппы шумят в панели как «проблемы с доменами» — после удаления обоих аппов bruzas с
created: 2026-08-10
sess: sess-0810i
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] `DeleteApp` не чистит `domain_hostnames` — мёртвые аппы шумят в панели как «проблемы с доменами» (sess-0810i, 2026-08-10, [live psql]) — после удаления обоих аппов bruzas строки `tvk-assistantbot-3d6fd8.dada-tuda.ru` (`failed/attach_timeout`) и `workassistantbot-9b7496.dada-tuda.ru` (`pending/route_missing`) остались в `domain_hostnames` с `managed=true` и попали в `domain_issues` `/api/v1/admin/overview`. Вторая строка получила апдейт `status_reason` **через минуту ПОСЛЕ** удаления аппа — значит фоновый reconciler продолжает трогать сирот и держит их «свежими» в панели. Это зеркало `project_deleteapp_orphans_domain_row_under_live_app.md`: там апп жив, а домена нет; тут домен жив, а аппа нет. Правка: каскад в `DeleteApp` (снять `managed` или удалить строку) + reconciler не должен реанимировать домены без App-снапшота.
