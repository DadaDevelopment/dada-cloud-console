---
id: 0256
status: open
prio: P0
title: P2-DBBACKUPS-ENV-FK-NOACTION sess-a951121a 2026-08-03 · Второй заряженный FK того же класса, найден при живой проверке прода
created: 2026-08-03
sess: sess-a951121a
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] P2-DBBACKUPS-ENV-FK-NOACTION sess-a951121a 2026-08-03 · Второй заряженный FK того же класса, найден при живой проверке прода [live psql]: `db_backups_environment_id_fkey|a` (NO ACTION) — то есть окружение, по которому хоть раз снимали бэкап, нельзя удалить (23503), и это бьёт по DeleteProject/teardown ровно как 044/093. НО фикс НЕ механический, поэтому отдельной строкой: `db_backups.environment_id` объявлен `NOT NULL` [code backend/migrations/029_db_backups.sql:9], значит SET NULL невозможен, а CASCADE сотрёт строки учёта бэкапов, оставив объекты в S3 сиротами (никто не подчистит, платим). Развилка: (а) снять NOT NULL + SET NULL (учёт живёт, но теряется привязка); (б) CASCADE + удаление объектов S3 в teardown (нужен delete-путь к бакету, а [memory project_s3_no_delete] говорит, что его нет). Решать по коду пути удаления бэкапов, не вслепую. Полный снимок delete-правил всех 19 FK на `environments` лежит в scratchpad-сессии.
