---
id: 0173
status: open
prio: P2
title: REAL-DB ТЕСТЫ gitops-agent ГОНЯЮТСЯ ПАРАЛЛЕЛЬНО ПО ОДНОЙ БАЗЕ — go test ./
created: 2026-08-11
sess: sess-0811c
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟡 REAL-DB ТЕСТЫ gitops-agent ГОНЯЮТСЯ ПАРАЛЛЕЛЬНО ПО ОДНОЙ БАЗЕ (sess-0811c, 2026-08-11, [замер]) — `go test ./...` с `TEST_DATABASE_URL` на холодную даёт `relation "git_repos" does not exist` / `duplicate key ... pg_type_typname_nsp_index`: пакеты `internal/db` и `internal/worker` применяют миграции одновременно в одну базу. `-p 1` зелёный, повторные прогоны зелёные (кеш). Гейт `probe-main-build.sh` этого не ловит, потому что гоняет gitops-agent БЕЗ `TEST_DATABASE_URL` — то есть real-DB тесты пакета там просто скипаются. Чинить в `applyMigrations` (адвизорный лок или схема на пакет), иначе Jenkins поймает это как «плавающий» красный.
