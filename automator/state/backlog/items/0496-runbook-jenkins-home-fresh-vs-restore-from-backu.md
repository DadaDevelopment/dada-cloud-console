---
id: 0496
status: open
prio: P2
stream: 2
title: runbook jenkins-home: fresh-vs-restore-from-backup ветка не описана
created: 2026-09-06
---
 runbook jenkins-home: ветка 'fresh vs restore-from-backup' не описана
09-05 после fleet-reroll jenkins-home взяли СОЗНАТЕЛЬНО свежим (churn races) - потеряли все jobs/org-folder/creds, CI стоял сутки. 09-06 восстановление из Longhorn-бэкапа pvc-62cbc693 (backup-a6f9922c41d64cf0, 09-04 05:01Z) прошло чисто: Volume CR fromBackup -> статический PV/PVC -> inspect-под -> проверить jobs/plugins/master.key -> копия в живой PVC -> рестарт. Хранить метод в argo-infra/runbooks/jenkins-home-longhorn-resilience.md рядом с fsck-шагом: свежий дом = только если НЕТ валидного бэкапа новее N часов. Проверять backupvolumes.longhorn.io список прежде чем брать fresh.
