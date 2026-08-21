---
id: 0050
status: open
prio: P2
title: УДАЛЁННЫЙ АПП НАВСЕГДА ОСТАВЛЯЕТ failed-ДОМЕН, И ПАНЕЛЬ СЧИТАЕТ ЭТО ЖИВОЙ ПРОБЛЕМОЙ
created: 2026-08-15
sess: sess-0815p
section: Backlog (execution-bet)
---
- [ ] 🟡 УДАЛЁННЫЙ АПП НАВСЕГДА ОСТАВЛЯЕТ `failed`-ДОМЕН, И ПАНЕЛЬ СЧИТАЕТ ЭТО ЖИВОЙ ПРОБЛЕМОЙ (sess-0815p, 2026-08-15, [live psql `domain_hostnames`], заземлено `origin/main@3d6379f9`) — в таблице НЕТ флага удаления (колонки: `id, authorization_id, environment_id, app_name, hostname, record_type, status, cert_status, operation_id, created_at, updated_at, managed, last_reissue_at, status_reason, attach_started_at, reattach_count` — `managed=t` это НЕ «удалён», на этом легко ошибиться). После `DeleteApp` строка остаётся `status=failed, status_reason='app_deleted'` вечно. Живьём таких 9 штук только от наших же проб (`excalidraw-probe`, `gl-anon-probe`, `wedge-probe`, `m2-live-probe`, `routine-upload-probe`, `ddc-cli-probe`, `libfix-probe`, `ddc-m2-probe` — все env `06675fff-445e-4425-aadb-3afbb4cdf35f`), плюс `m2-delwedge-6ccb0a` (`attach_timeout`, висит с 07-31). Именно `m2-delwedge` каждый цикл всплывает в `domain_issues` админ-панели и требует ручной классификации «наше/юзерское». Мусор копится монотонно → бакет со временем перестанет читаться, и настоящая сломанная выдача юзера утонет в нашем скрапе. Фикс = либо `DeleteApp` дочищает `domain_hostnames`, либо панель фильтрует `status_reason='app_deleted'`; первое честнее. Пруф = `domain_issues` содержит только домены живых аппов. Смежное: [[project_deleteapp_orphans_domain_row_under_live_app]] (там домен под ЖИВЫМ аппом — другой случай, не путать).
