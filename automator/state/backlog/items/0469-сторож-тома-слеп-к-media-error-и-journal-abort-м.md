---
id: 0469
status: open
prio: P1
title: Сторож тома слеп к media error и journal abort: меряет только байты и inode
created: 2026-08-21
sess: sess-0822c
---
[code backend/internal/api/app_volume_watcher.go:214,272,279,284,398]

`tick` тянет из Prometheus ровно две пары серий - `kubelet_volume_stats_used_bytes/capacity_bytes`
и `..._inodes_used/inodes`. `hotVolumeSamples` и `maybeNotify` алертят по двум ratio >= 0.85.
Ни `container_fs_errors_total`, ни `node_filesystem_errors_total`, ни признака read-only remount.

Третий класс отказа - физическая ошибка носителя (Errno 5, critical medium error, journal abort) -
проходит мимо сторожа целиком. Ровно как раньше проходила inode-авария. Это тот же блайндспот
третий раз подряд: сторож видит только те оси, по которым его однажды научили.

Нужен третий `ratio_kind` (например `media_error`) с текстом баннера, который говорит правду:
«расширение диска не поможет, это аппаратная поломка носителя, нужен перенос тома».
