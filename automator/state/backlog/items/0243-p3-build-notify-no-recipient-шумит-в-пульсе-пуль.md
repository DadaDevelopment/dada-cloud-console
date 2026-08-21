---
id: 0243
status: open
prio: P0
title: P3-BUILD-NOTIFY-NO-RECIPIENT-ШУМИТ-В-ПУЛЬСЕ · Пульс поймал первую в истории строку SendBuildNotification c outcome=failure, reason
sess: sess-0802t
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟢 P3-BUILD-NOTIFY-NO-RECIPIENT-ШУМИТ-В-ПУЛЬСЕ (sess-0802t, разобрано агентом-дебагером, клиентского риска НЕТ) · Пульс поймал первую в истории строку `SendBuildNotification` c `outcome=failure, reason=no_recipient` (02-08 12:51:24 UTC, билд `4c5ecbe1`, app `dada-development-site`). **Не инцидент [live psql]:** проект `internal` (org `dada`) имеет `owner_id IS NULL` — это наш служебный проект, билд руками запустил `alexkekiy@dada-tuda.ru`. Масштаб за 30д: 30 упавших билдов, из них 8 без адресата — ВСЕ в org `dada` (`reels-tracker` ×6, `dada-development-site` ×2), клиентов 0. **Корень [code]:** `OwnerEmail()` [build-agent/internal/db/notify.go:19-35] делает INNER JOIN `projects.owner_id → users`, при NULL получает ErrNoRows и возвращает `("", nil)`; `runner.go:432-439` пишет `no_recipient`. Тот же провал в консоли уже лечили цепочкой фолбэков `owner_id → project_members → org-username → аудит оператору` [backend/internal/api/app_health_watcher.go:298-311, комментарий :280-284 прямо про «5 live projects have owner_id NULL»], build-agent этот фикс не унаследовал. · **Делать когда дойдут руки:** либо переиспользовать резолвер из watcher'а, либо (дешевле) отдавать `outcome=skipped, reason=system_project` для проектов без owner_id, чтобы пульс не звенел ложным «клиент не узнал о падении». Приоритет низкий именно потому, что за 30д ни один клиент не пострадал.
