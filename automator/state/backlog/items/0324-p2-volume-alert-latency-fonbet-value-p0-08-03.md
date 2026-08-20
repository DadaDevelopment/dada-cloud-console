---
id: 0324
status: open
prio: P0
stream: 6
title: P2-VOLUME-ALERT-LATENCY · fonbet-value P0 08-03
created: 2026-08-03
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] P2-VOLUME-ALERT-LATENCY · fonbet-value P0 08-03: 85%-порог алерта на заполнение диска настроен правильно (`backend/internal/api/app_volume_watcher.go:27` `appVolumeAlertThreshold = 0.85`), но тикер `appVolumeAlertThreshold` ходит раз в 15 мин (`app_volume_watcher.go:23` `appVolumeWatchInterval = 15*time.Minute`) — если том заполняется 85%→100% быстрее 15 минут (как у fonbet-value: скрейпер валит raw_archive пачками), алерт срабатывает уже на ratio=0.9992, практически синхронно с ENOSPC-крашем, а не как ранее предупреждение. Не чинил (правило одного яка в рамках P0-цикла) — либо сократить интервал тикера для аппов с историей близких P0 (адаптивный poll), либо завести отдельный быстрый триггер на скорость роста (df delta/мин), а не только абсолютный ratio.
