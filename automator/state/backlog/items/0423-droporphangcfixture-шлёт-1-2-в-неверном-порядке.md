---
id: 0423
status: open
prio: P2
stream: 2
title: dropOrphanGCFixture шлёт $1/$2 в неверном порядке — уборка фикстур молча не работает
created: 2026-08-20
sess: sess-0821c
---
Найдено попутно sess-0821c [code]. `gitops-agent/internal/worker/statusreconciler_orphan_gc_test.go`,
функция `dropOrphanGCFixture`: часть DELETE-ов получает параметры в неверном порядке/количестве,
рантайм печатает `orphan-gc fixture cleanup failed (DELETE FROM projects WHERE id = $1): mismatched
param and argument count` на КАЖДОМ тесте, использующем фикстуру (воспроизведено в прогоне
TestReconcile_RecoveredAppClearsStaleCrashTail).

Тесты при этом зелёные — значит риг копит непочищенные проекты/окружения в локальной тест-базе.
Не чинил: один як на цикл, к 0422 отношения не имеет.
