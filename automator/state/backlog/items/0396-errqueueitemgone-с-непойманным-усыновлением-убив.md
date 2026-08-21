---
id: 0396
status: open
prio: P1
stream: reliability
title: ErrQueueItemGone с непойманным усыновлением убивает сборку сразу, без грейса
created: 2026-08-20
sess: sess-0820d
---
build-agent/internal/worker/runner.go:1336-1345: на ErrQueueItemGone, если FindBuildByQueueID
вернул ошибку или ok=false, функция возвращает ошибку немедленно — без ретрая и грейса,
в отличие от ветки default двумя строками ниже (queueErrGrace, 2 минуты).
Три прод-сборки умерли так уже ПОСЛЕ раскатки образа 293013d2:
9a5741a8 19:01:39, d642e345 19:31:43, 0a823dbb 20:04:42 — все
"resolve build number: queue item NNNNN: queue item no longer known to jenkins".
