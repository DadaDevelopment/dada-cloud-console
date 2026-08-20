---
id: 0361
status: open
prio: P0
stream: 6
title: ReattachOrphanedHostnames НЕ МОЖЕТ ПОДНЯТЬ reels-tracker-fe2427 ПО ПОСТРОЕНИЮ
created: 2026-08-13
sess: sess-0813n
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🔴 `ReattachOrphanedHostnames` НЕ МОЖЕТ ПОДНЯТЬ `reels-tracker-fe2427` ПО ПОСТРОЕНИЮ (sess-0813n, 2026-08-13, [live psql + code]) — строка так и `failed/app_deleted`, `updated_at` 08-10 14:47 не менялся после выкатки `5432a7e6`. Джойн `backend/internal/api/domains.go:1839-1858` требует `rs.environment_id = dh.environment_id`, а домен висит на environment проекта `platform` (`dcc645f6-2b4a-458b-8f72-682bb6f680a6`), тогда как ЖИВОЙ App-снапшот `reels-tracker` лежит под `internal`, env `20000000-0000-0000-0000-000000000002` (тот же владелец). Самолечение по имени+environment слепо к переезду аппа между окружениями. Решать: либо реаттач ищет живой App по имени в пределах ВЛАДЕЛЬЦА/проекта и переписывает `environment_id`, либо (безопаснее) отдельно помечать «домен указывает на окружение, где такого аппа никогда не было» — сейчас это молча вечный `failed`. Проекты `platform`/`internal` read-only, руками не трогал.
