---
id: 0456
status: open
prio: P1
stream: 2
title: app_health_watcher молчит 3 дня о повторяющемся app_code-краше, если между падениями под успевает стать Ready
created: 2026-08-21
sess: sess-0821i
---
sevarateambot в sevarabot-prod [live 2026-08-21]: app_health_alerts.last_sent_at = 2026-08-18 22:43, last_seen_at = 2026-08-21 20:12, last_send_ok=t. Кулдаун appHealthAlertCooldown=24h (backend/internal/api/app_health_watcher.go:28) ТЕОРЕТИЧЕСКИ разрешает переслать, но новых строк SendNotification/AppHealthAlert в audit_events после 18-го НЕТ ни одной. Паттерн падения -- exit 1 раз в ~2ч12м, между падениями Ready=True: сторож не застаёт под нездоровым и не берёт новый слот. Юзер три дня не знает, что его бот падает. Починка: считать повторные Error-терминации одного контейнера за окно, а не только застигнутый CrashLoopBackOff.
