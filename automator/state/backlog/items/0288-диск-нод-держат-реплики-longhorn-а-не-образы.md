---
id: 0288
status: open
prio: P2
title: ДИСК НОД ДЕРЖАТ РЕПЛИКИ LONGHORN, А НЕ ОБРАЗЫ
created: 2026-08-12
sess: sess-0812a
section: Открытые долги (не терять)
---
- [ ] 🟡 ДИСК НОД ДЕРЖАТ РЕПЛИКИ LONGHORN, А НЕ ОБРАЗЫ (sess-0812a, 2026-08-12, [live nsenter du], argo-infra@console-migration `e914fef5`) — на всех 4 нодах image-fs 72-94%, kubelet пишет `FreeDiskSpaceFailed` и освобождает 0 байт. Причина: на zh58h `/var/lib/longhorn` = 54G из ~98G занятых при 11G всех образов вместе. Интервал node-image-prune поднят 6h→30m (`e914fef5`) — это смягчение. Реальный рычаг owner-gated: вынести longhorn data-path на отдельный диск / расширить vda1 / разбалансировать реплики. Дешёвая проверка ДО этого: не висят ли на нодах неудалённые снапшоты Longhorn (`project_longhorn_deleted_snapshot_keeps_disk`).
