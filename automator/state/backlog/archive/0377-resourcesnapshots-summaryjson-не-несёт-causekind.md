---
id: 0377
status: closed
prio: P1
stream: 3
hypothesis: H08
title: resource_snapshots.summary_json не несёт cause_kind/cause_line — карточка аппа причины не знает
created: 2026-08-19
sess: sess-0820a
closed_at: 2026-08-20
closed_commit: 88430035
closed_note: Премиса неверна [code]: карточка аппа берёт причину из app_health_alerts (apps.go:494 -> loadAppAlerts, app_alerts.go:107 cause_kind/cause_line), а не из summary_json; чип рендерит cause_kind (apps/page.tsx:714). Зеркалить в снапшот не нужно. Реальная дыра была в протухшем reason — закрыта 0422/88430035
---
**Заземлено [live psql], 2026-08-19.** Обогащение причины краша живёт ТОЛЬКО в `app_health_alerts`.

- `fonbet-value`: `app_health_alerts.cause_kind='platform_storage'`, `cause_line` с ENOSPC — есть.
  `resource_snapshots.summary_json` того же аппа (`last_synced_at` 21:12:04Z) несёт только
  `status=CrashLoop, reason=CrashLoopBackOff, exit_code=1, http_status=503, restarts=10`.
- `gulyaev-ai-core`: в `summary_json` `cause_kind`/`cause_line` пусты при `restarts=117`.

Значит любой читатель, берущий причину из снапшота (карточка приложения, списки), показывает голое
`CrashLoopBackOff` при том, что развёрнутый диагноз у нас уже посчитан и лежит рядом.

Проверить, ОТКУДА читает причину страница аппа, и если из снапшота — зеркалить туда `cause_kind`/
`cause_line` тем же стейтментом, что пишет алерт (память: сигнал — тем же стейтментом).
