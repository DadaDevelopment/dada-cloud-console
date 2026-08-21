---
id: 0004
status: closed
prio: P0
title: dd419d4e · ДОСТАВЛЕНО В ПРОД (Jenkins #1230 SUCCESS, образ :dd419d4e живой в argocd-prod, бэкенд 1/1 Running) · 🔴 УДАЛЕНИЕ АППА УБ
created: 2026-08-19
sess: sess-0819b
section: Backlog (execution-bet)
closed_at: 2026-08-19
closed_commit: dd419d4e
---
- [x] ЗАКРЫТ sess-0819b 2026-08-19 · `dd419d4e` · ДОСТАВЛЕНО В ПРОД (Jenkins #1230 SUCCESS, образ `:dd419d4e` живой в `argocd-prod`, бэкенд 1/1 Running) · 🔴 УДАЛЕНИЕ АППА УБИВАЛО ЕГО ЖЕ ЛЕТЯЩУЮ СБОРКУ, И ЮЗЕР ВИДИТ НУЛЕВОЙ UUID (ПЕРЕЗАЗЕМЛЕНО sess-0819b, 2026-08-19, [live psql `audit_events`+`builds`+`pg_constraint`], origin/main@b2a9a842). 🔴 ИСХОДНАЯ ФОРМУЛИРОВКА ОПРОВЕРГНУТА ПО ДВУМ ПУНКТАМ. (1) `ConnectGitRepo` НЕ врёт: провал вставки возвращает `link_insert_failed`/`outcome=failure`, а все семь коннектов `kkartov` на аппе `instatic` — честные `success`. (2) FK-каскад с проекта/окружения НЕ срабатывал: строки `projects`/`environments` целы, события `DeleteProject` у него нет вообще. ПРАВДА ПО `operation_id`: за каждым коннектом через 1-40 минут идёт его же `DeleteApp` (actor = сам юзер `fd84a5e7…`, не рипер), последний — 08-19 03:18:14, после него он не возвращался. Пустой `git_repos` — это by design: `gitops-agent/internal/worker/dbwatcher.go:1352` сносит строку намеренно, чтобы квоты и `ListApps` не синтезировали фантом. ✅ НАСТОЯЩИЙ ДЕФЕКТ, КОТОРЫЙ ОСТАЁТСЯ: удаление аппа при ЛЕТЯЩЕЙ сборке роняет `builds.git_repo_id` в NULL (`builds_git_repo_id_fkey` = `ON DELETE SET NULL`, миграция 116, сверено по живому `pg_constraint`), билд-агент читает его в непойнтерный `uuid.UUID`, получает нули и показывает человеку `load repo 00000000-0000-0000-0000-000000000000: no rows in result set`. Наблюдено на трёх реальных сборках 08-18 (23:46:20, 23:47:01, 23:49:02). Чинить в `backend/internal/api/delete_impact.go` (`DeleteApp`): в той же транзакции, что и `demoteAppHostnames`, помечать летящие сборки этого аппа терминально и честной причиной («апп удалён»), а не оставлять их гоняться за снесённой привязкой.
