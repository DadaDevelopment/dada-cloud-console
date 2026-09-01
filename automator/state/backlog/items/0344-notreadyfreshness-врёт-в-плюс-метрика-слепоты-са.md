---
id: 0344
status: open
prio: P0
stream: 6
title: not_ready_freshness ВРЁТ В ПЛЮС: метрика слепоты сама слепая
created: 2026-08-10
sess: sess-0810m
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🔴 `not_ready_freshness` ВРЁТ В ПЛЮС: метрика слепоты сама слепая (sess-0810m, 2026-08-10, [live api+psql]) — панель отдаёт `blind:false`, `newest_sync_age_seconds:9`, `stale_apps:1`, потому что берёт САМУЮ СВЕЖУЮ строку по всей таблице. Под этим «здорово» лежат 39 строк `resource_snapshots kind='PublicApi'` с `phase<>'Ready'` и `last_synced_at` от 16 часов до **27 суток 22 часов**. Одна живая строка маскирует заморозку всей таблицы. Это ровно класс `project_admin_broken_panel_read_health_from_own_blindness`: «пусто/зелено» означает «сборщик не смог», а не «поломок нет». Правка: считать свежесть по ХУДШЕЙ строке (или по доле строк старше окна), а не по лучшей. Гейт обязателен — иначе метрика опять будет мерить сама себя.
