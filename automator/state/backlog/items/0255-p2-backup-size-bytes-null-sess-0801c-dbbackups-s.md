---
id: 0255
status: open
prio: P0
title: P2-BACKUP-SIZE-BYTES-NULL sess-0801c · db_backups.size_bytes не заполняется НИКОГДА (все 10 плановых строк с NULL )
sess: sess-0801c
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] P2-BACKUP-SIZE-BYTES-NULL sess-0801c · `db_backups.size_bytes` не заполняется НИКОГДА (все 10 плановых строк с NULL [live psql]) — объём отгруженного в S3 из БД не виден, «бэкап есть» нельзя отличить от «бэкап пустой». Живой пример: `zerkalo` = 1161 байт. Байты берутся head_object'ом по `dump_path` (профиль-префикс `k10/postgresql-logical/` + путь), presigner уже есть [code db_backups.go]. Дёшево и делает бэкап проверяемым в UI.
