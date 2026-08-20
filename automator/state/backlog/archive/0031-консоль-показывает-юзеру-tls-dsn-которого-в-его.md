---
id: 0031
status: closed
prio: P0
hypothesis: H02
title: КОНСОЛЬ ПОКАЗЫВАЕТ ЮЗЕРУ TLS-DSN, КОТОРОГО В ЕГО АППЕ НЕТ
created: 2026-08-16
sess: sess-0816a
section: Backlog (execution-bet)
closed_at: 2026-08-16
closed_commit: 8ef4b802
---
- [x] ОТГРУЖЕНО sess-0816a `8ef4b802` (2026-08-16, подхвачен незакоммиченный WIP sess-0815r с истёкшим локом; RED показан выводом — с занулённым `supersedesPlatformIssuedDSN` падают ровно 2 ветки `wrote = false, want true`; GREEN — весь `internal/api` на real-DB риге, ok 22.380s; `seedEnvVar` уже был `ON CONFLICT DO UPDATE`, миграция не нужна) · 🔴 КОНСОЛЬ ПОКАЗЫВАЕТ ЮЗЕРУ TLS-DSN, КОТОРОГО В ЕГО АППЕ НЕТ (sess-0815r, 2026-08-15, [live: kubectl secrets + MCP `getDatabaseCredentials` + code], hypothesis: H02, origin/main@7990299d) — после включения `MANAGED_DB_TLS_DSN_ENABLED=true` эндпоинт кредов отдаёт `postgresql://…@db.pv.dada-tuda.ru:5432/…?sslmode=require`, а в секретах ОБОИХ живых юзерских аппов с базой лежит замороженная строка со старым хостом и вообще без `sslmode`: `artempro2021-bk-ru-prod/fanvk-env` → `pg-router.databases.svc.cluster.local`, `artempro2022-yandex-ru-prod/megafactory-env` → то же (сверено base64-декодом секретов, 2 из 2). Причина в продукте: `backend/internal/api/db_dsn_delivery.go:157-171` `seedDatabaseDSNIfAbsent` сторожит `appEnvVarIsSet(…,"DATABASE_URL")` и выходит с аудитом `{"seeded":false,"reason":"already_set"}` — платформа НИКОГДА не обновляет собственную выданную строку. Следствие для юзера: страница базы показывает одно, приложение подключается другим; каноничный `ssl: true` бьётся о хост без TLS, а «поправить» нечем — ровно форма [[project_diagnosis_without_a_lever_leaves_user_down]]. Правка: обновлять DSN, только если сохранённое значение опознаётся как ВЫДАННОЕ ПЛАТФОРМОЙ для той же базы (совпали схема, username, password, имя базы; хост из известного набора `pg-router…`/`db.pv.dada-tuda.ru`/`pg-shard-*…`) — иначе не трогать и писать аудит `user_modified`.
  ПОПРАВКА К СОБСТВЕННОМУ ЗАМЕРУ: сообщение суб-агента, что продукт выдаёт `pg-shard-0-postgresql.databases.svc.cluster.local`, опровергнуто живым эндпоинтом — все 5 проверенных connection-секретов несут `endpoint=pg-router.databases.svc.cluster.local`, а `getDatabaseCredentials` вернул `db.pv.dada-tuda.ru`. Хост шарда — это как раз замороженное старое значение из env аппа, а не то, что выдаётся сейчас.
