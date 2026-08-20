---
id: 0115
status: open
prio: P0
title: НОДЫ ПЕРЕПОДПИСАНЫ ПО ПОСТРОЕНИЮ: LONGHORN НЕ ЗНАЕТ ПРО CONTAINERD
created: 2026-08-13
sess: sess-0813c
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🔴 НОДЫ ПЕРЕПОДПИСАНЫ ПО ПОСТРОЕНИЮ: LONGHORN НЕ ЗНАЕТ ПРО CONTAINERD (sess-0813c, 2026-08-13, [live: пофайловый обход zh58h + `nodes.longhorn.io`]) — на `zh58h` 99.0 из 105.5 GiB заняты: 39.6 реплика `databases/postgresql-0`, 39.4 containerd (28.6 overlayfs + 10.4 blobs), 9.2 prometheus, 4.4 pg-shard-0, 1.4 /var/log. Longhorn наскедулил 60 GiB реплик на root, где уже жило 39.4 GiB containerd, потому что `storageReserved` стоял 10.55 GiB при реальном не-Longhorn потреблении ~43 GiB. Это не инцидент, это дефолт, который повторится на любой ноде. СДЕЛАНО в этом цикле: `storageReserved` на zh58h поднят до 45 GiB, живой пруф — узел ушёл в `Schedulable: False / DiskPressure`, реплики не тронуты. ОСТАЁТСЯ: тот же аудит и та же поправка на остальных трёх нодах (`xnwvp` 13.3% свободного — следующий кандидат), и решение, откуда брать `storageReserved` — вручную на каждой ноде это отложенный P0, нужен либо расчёт при инициализации, либо алерт на расхождение «reserved vs реальное не-Longhorn потребление». Опровергнутая по дороге гипотеза, не переоткрывать: резерв в 25.85 GiB по `markRemoved`-снапшотам — ФАНТОМ, 21 из 22 прямой родитель `volume-head` и не схлопывается по конструкции; штатный `snapshotPurge` дёрнут по 17 томам, 200 OK, освобождено ноль. Образы тоже мимо: непривязанных 0.1 GiB. Крупный рычаг — два снапшота платформенного postgres без `markRemoved` на 35.5 GiB — ушёл в `owner-actions.md`, необратимое удаление данных не моё решение. Смежное: `project_node_disk_is_eaten_by_legit_replica_data_not_images.md`, `project_ci_agent_shared_node_disk_with_platform_postgres.md`.
