---
id: 0468
status: closed
prio: P0
title: fonbet-value лежит на диске с critical medium error: реплика Longhorn без резерва
created: 2026-08-21
sess: sess-0822c
closed_at: 2026-08-22
closed_commit: 8de3edf
closed_note: апп поднят: e2fsck чист (EXIT=0, 0 исправлений), сплошное чтение 20ГБ без ошибок — диагноз «аппаратные бэд-секторы» ОПРОВЕРГНУТ; причина ухода реплики открыта в 0472, отсутствие резерва данных в 0471
---
[live dmesg ноды d5c373-client-b2a7cd-7qwnw-rt7fr, 2026-08-21]

Апп `fonbet-value` (ns `artemmendeleev-gmail-com-prod`, владелец artemmendeleev@gmail.com,
активен <48ч) в краш-лупе с `OSError: [Errno 5]`. Гипотеза «кончился диск/inode» НЕ подтвердилась:
bytes 28.8% (6.0/21.0 GB), inodes 82.0% (1074857/1310720, свободно 235863).

Настоящая причина - аппаратные бэд-секторы под Longhorn-репликой:

    critical medium error, dev sde, sector 29421384 op WRITE
    EXT4-fs error (device sde): ext4_journal_check_start:84: Detected aborted journal
    EXT4-fs (sde): Remounting filesystem read-only

Реплика `pvc-3f5c3c0f-...-r-25e46e04`, нода `d5c373-client-f675c9-npkxg-xnwvp`,
disk `default-disk-84f8d30a809b9990`. StorageClass `longhorn-dev` имеет `numberOfReplicas: 1` -
здоровой копии не существует, Longhorn восстановиться не с чего. `robustness: healthy` в CR тома
врёт: Longhorn меряет здоровье своего процесса, а не ext4 внутри реплики.

Расширение PVC (`UpdateAppStorage`) бесполезно - диск не полон, а повреждён.

Рычаг только платформенный: пометить диск unschedulable и вырастить реплику на здоровой ноде.
ОСТОРОЖНО - источник копирования это и есть битый диск, эвакуация может частично не прочитаться.
Перед действием снять снапшот и решать по данным, а не вслепую. У юзера такого рычага в продукте
нет вообще.

Второй вывод для платформы: `numberOfReplicas: 1` на пользовательских томах означает, что любой
аппаратный отказ диска = безвозвратная потеря данных юзера.
