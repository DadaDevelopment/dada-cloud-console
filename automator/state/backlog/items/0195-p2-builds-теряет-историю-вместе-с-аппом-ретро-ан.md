---
id: 0195
status: open
prio: P0
title: P2 builds теряет историю вместе с аппом, ретро-аналитика оттока структурно невозможна
created: 2026-08-07
sess: sess-0807d
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] P2 `builds` теряет историю вместе с аппом, ретро-аналитика оттока структурно невозможна (sess-0807d, 2026-08-07) — `backend/migrations/013_git_build_deploy.sql:58` `builds.git_repo_id … ON DELETE CASCADE`, `git_repos.project_id/environment_id` (`:30-31`) тоже CASCADE. Удаление аппа стирает все билды юзера. Проверено [live psql]: у 2 из 8 билдивших юзеров окна (25%) `audit_events` помнит успешный `TriggerBuild`, а `builds`-строк ноль — это те двое, чьи демо снесли 07-30. Следствие: воронка по `builds` занижает именно ушедших, чем сильнее отток, тем чище метрика. Обратное расхождение там же: у `artempro2021` 11 `builds` при 4 `TriggerBuild` — авто-ребилды по коммиту в audit не пишутся. Чинить soft-delete (`deleted_at`) или отдельным `build_events`-логом, переживающим ресурс. Пока обходится счётом по `audit_events` — см. `state/audit-path-graph.md` §8.
