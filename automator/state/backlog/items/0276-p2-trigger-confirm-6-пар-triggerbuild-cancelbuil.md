---
id: 0276
status: open
prio: P3
stream: 2
title: P2-TRIGGER-CONFIRM · 6 пар TriggerBuild -> CancelBuild за 2-9 секунд у 3 юзеров = рефлекторный запуск не того (ветка/коммит/env)
section: 🎯 ПОТОК 2 — Deploy speed & reliability как продукт
---
- [ ] P2-TRIGGER-CONFIRM · 6 пар `TriggerBuild -> CancelBuild` за 2-9 секунд у 3 юзеров = рефлекторный запуск не того (ветка/коммит/env). Превью ветки+коммита+изменившихся env перед запуском (`frontend/app/(console)/projects/[projectId]/apps/[appName]/deployments/page.tsx:105-118`) уберёт мусорные queued-строки в `builds` и лишний шум в графе пути. Дешёвая правка, не срочная.
