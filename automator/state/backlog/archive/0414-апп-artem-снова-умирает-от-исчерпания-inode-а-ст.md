---
id: 0414
status: closed
prio: P0
stream: 2
hypothesis: H02
title: Апп artem снова умирает от исчерпания inode, а сторож тома молчит второй раз подряд
created: 2026-08-20
sess: sess-0820e
closed_at: 2026-08-20
closed_commit: 7ecaa9db
closed_note: Рычаг отгружен: compact-Job (POST/GET .../volume/maintenance/compact) пакует каталог в tar.gz на том же томе и удаляет оригиналы ТОЛЬКО после проверки архива; в консоли кнопка "Упаковать в архив" на строке top_dirs за модалкой. Сторож НЕ был сломан второй раз: фикс 9e8ba9c7 доехал и VolumeAlert выстрелил 2026-08-19 22:11 UTC; 9 суток тишины дал байтовый слепой пятак + ресайз юзера 10->20Gi (доля байт упала до 0.73, inode остались 1.000). ЧЕСТНЫЙ ПРЕДЕЛ: живого прогона Job в проде НЕ было, ни одного inode на fonbet-value не освобождено, апп всё ещё в CrashLoopBackOff.
---
Триаж sess-0820e [live kubectl]: `fonbet-value` (Default, artemmendeleev@gmail.com)
падает с `OSError: [Errno 28] No space left on device` при записи в
`/data/raw_data/bodies/sha256/...`.

Правда о томе: `df -h /data` = 74% занято (5.3 ГиБ свободно), `df -i /data` =
1310720/1310720 inode, **IUse 100%, ноль свободных**. Байты не при чём.

PVC `fonbet-value-pvc`, 20Gi RWO, storageclass `longhorn-dev` — потолок inode
фиксирован классом хранения и от аппа не зависит. Апп с 2026-08-10 (10 суток)
крутится в цикле краш/самолечение, 36 рестартов за ~4 часа.

ЭТО ВТОРОЙ СЛУЧАЙ того же класса — см память
`project_volume_watcher_measured_bytes_and_was_blind_to_inode_exhaustion.md`
(помечена как закрытая ✅). Значит первый фикс либо не доехал до прода, либо
покрывает не тот путь. ПЕРВЫМ делом проверить доставку того фикса
(`is-ancestor` против тега РАБОТАЮЩЕГО пода), а не писать новый.

Второе: у юзера нет рычага. Исчерпание inode из консоли не видно и не чинится.
