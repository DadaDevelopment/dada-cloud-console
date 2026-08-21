---
id: 0113
status: open
prio: P1
title: РАННБУК ВОССТАНОВЛЕНИЯ jenkins-home НЕ ПРОВЕРЯЕТ ФАЙЛОВУЮ СИСТЕМУ, А LONGHORN «HEALTHY» ЕЁ НЕ ГАРАНТИРУЕТ
created: 2026-08-13
sess: sess-0813c
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟠 РАННБУК ВОССТАНОВЛЕНИЯ `jenkins-home` НЕ ПРОВЕРЯЕТ ФАЙЛОВУЮ СИСТЕМУ, А LONGHORN «HEALTHY» ЕЁ НЕ ГАРАНТИРУЕТ (sess-0813c, 2026-08-13, [live]) — том, поднятый `fromBackup`, был `attached`/`healthy`, но ext4 внутри битая: `fsck` в preen отказался чинить (`UNEXPECTED INCONSISTENCY; RUN fsck MANUALLY`), CSI `MountDevice` падал, под не стартовал вообще. Симптом приезжает как «под не поднялся», а не как «диск битый» — и читается как злая проба. Второй раз, когда `healthy` маскирует порчу ФС. ДЕЙСТВИЕ: в `argo-infra/runbooks/jenkins-home-longhorn-resilience.md` между шагом 6 и 7 вставить проверку ФС (`e2fsck -fn` на `/dev/longhorn/<pvc>` из привилегированного пода на WORKLOAD-ноде, не на `currentNodeID` движка) — иначе следующий восстановленный том встанет так же. Память `project_longhorn_healthy_volume_masked_ext4_corruption`.
