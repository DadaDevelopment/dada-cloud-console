---
id: 0143
status: open
prio: P2
title: ДИСК НОД 82-95%, СОБЫТИЯ FreeDiskSpaceFailed НА ТРЁХ НОДАХ — image filesystem 95%/83%/82%
created: 2026-08-12
sess: sess-0812d
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟡 ДИСК НОД 82-95%, СОБЫТИЯ `FreeDiskSpaceFailed` НА ТРЁХ НОДАХ (sess-0812d, 2026-08-12, [live kubectl events], origin/main@29283cec) — image filesystem 95%/83%/82%. Юзера сегодня не блокирует, но это тот же корень, что уже дважды ронял платформу (`project_node_disk_full_killed_platform_postgres`, `project_platform_postgres_disk_full_p0`), и он же кормит клины Longhorn 08-11. Известный рычаг из прошлого цикла: интервал node-image-prune 6h→30m (`e914fef5`) плюс реплики Longhorn держат 54G из 98G — чистить надо не образы, а сироты-реплики (`probe-longhorn-orphans.sh`).
