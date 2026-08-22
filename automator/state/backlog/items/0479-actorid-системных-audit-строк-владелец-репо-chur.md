---
id: 0479
status: open
prio: P1
stream: 4
title: actor_id системных audit-строк = владелец репо, churn занижался на 269ч
created: 2026-08-22
sess: sess-0822h
---
`build-agent/internal/db/builds.go:358-360` и `deploy.go:354-362` (`handoffActor()`) пишут `COALESCE(triggered_by, created_by, zero-uuid)`: автопересборка по вебхуку получает `actor_id` владельца репо при `actor_type='system'`.

Числовое доказательство [live 2026-08-22]: naive `MAX(created_at)` по `actor_id` показал sergeykozlov2006@gmail.com живым 08-20 при реальной тишине с 08-09 — искажение 269.2ч; lifecoachrussia@yandex.ru — 21.6ч. Подтверждённый churn 30д-когорты вырос 3 → 5 только от исправления метода замера.

Эталон правильного поведения: `backend/internal/api/audit.go:446-448` → жёсткий `systemDeployActorID` (`backend/internal/api/deploy_hooks.go:1`).

Чинить: убрать `created_by`-фолбэк в двух файлах. Старые строки фиксом записи не переписываются, поэтому в любом запросе «последнее действие юзера» обязателен `WHERE actor_type='user'`.
