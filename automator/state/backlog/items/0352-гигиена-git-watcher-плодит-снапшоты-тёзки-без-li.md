---
id: 0352
status: open
prio: P3
stream: 6
title: ГИГИЕНА: git-watcher плодит снапшоты-тёзки без live_source при проигрывании истории репо
created: 2026-08-11
sess: sess-0811d
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🔵 ГИГИЕНА: git-watcher плодит снапшоты-тёзки без `live_source` при проигрывании истории репо (sess-0811d, 2026-08-11, [live psql]) — `gitwatcher.go:717` (`syncChartTemplate`) вставляет строки с `phase="Unknown"` и БЕЗ `live_source`, штампуя `last_synced_at = c.When` (время коммита), отсюда `last_synced_at` (2026-07-13) < `first_seen_at` (2026-08-03). Именно так `example-project` и `devtools` перехватили имена `n8n`/`svod` у настоящих владельцев (`platform`/`internal`) 2026-08-03 17:00:52, через 3.4 с после последнего удачного синка — и заморозили их на 7 суток. Тай-брейк починен (`497a3147`), но источник тёзок остался: заглушки без `live_source` в панель не попадают, зато ломают матчинг по имени. Решить, должен ли рендер helm-шаблона вообще создавать строку снапшота для cluster-scoped рода.
