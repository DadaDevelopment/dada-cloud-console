# ops-handoff 2026-09-26T04:15Z

MAIN: green (1a68aef9, Jenkins DADA-GH/dada-cloud-console/main #157 SUCCESS)
[live: Jenkins getBuild #157 result=SUCCESS, sha=1a68aef9; probe-main-build.sh
full pass after fix - go build/vet backend, build-agent, gitops-agent all OK,
go test gitops-agent OK, go test backend SKIP (no local real-DB rig on
127.0.0.1:55432, see memory reference_local_realdb_test_rig), frontend
test:unit + lint OK]

DISK INCIDENT (my P0, fixed): VM was at 300MiB free (90-100% overlay /),
probe-main-build.sh FAILed go build/vet/test on gitops-agent with
"no space left on device" - not a code issue, PROBE-ENV-BROKEN. Root cause:
accumulated exited containers + unused docker images (kafka/clickhouse/
plantuml/ryuk, none referenced by any live container - verified with
`docker ps -a --filter ancestor=<id>` = empty before removal). Fix: removed
2 exited scratch containers + pruned dangling images + rmi'd 5 unused old
images -> freed ~8.2GB (300MiB -> 8.1GB free) [live: df -h before/after,
docker system df -v, docker image prune -f, docker rmi]. Re-ran
probe-main-build.sh clean after the fix - all green.

DELIVERY: доставлено полностью (живой тег 1a68aef9 == origin/main, 0 строк
кода сверху) [live probe-delivery.sh]

PANEL: not_ready=3, other=4, domains=3, failed_builds=3, blind=False
[live pulse-remote.sh, snapshot age 54 min at run time; stuck_operations=0,
errors empty]

- not_ready:
  - nodejs-argo (CrashLoopBackOff, owner wow83168@gmail.com) - юзерское, не трогал
  - gulyaev-ai-core (CrashLoop/Error, owner lifecoachrussia@yandex.ru, http=503
    on live URL) - юзерское, не трогал
  - **kube-prometheus-stack (Observability project, owner alexkekiy@icloud.com)
    - ЭТО НАШЕ.** Diagnosed live via kubectl: pod
    prometheus-monitoring-stack-prometheus-0 CrashLoopBackOff, container panics
    on startup: `open /prometheus/queries.active: input/output error` ->
    `panic: Unable to create mmap-ed active query log`. Root cause traced to
    the storage layer, not the app: the backing Longhorn volume
    (pvc-be1ac09d-e0ee-4fed-aec3-32ab39a5d99b, 8Gi, replicas=1, no HA) is
    `robustness: faulted`, `state: detached`. Its single replica is `stopped`
    with condition `RebuildFailed`. longhorn-manager is stuck in a tight
    auto-salvage retry loop ("All replicas are failed" -> "Bringing up 0
    replicas for auto-salvage", repeating every ~10-30s for at least the last
    hour of logs) - it never actually brings the replica back up. Node
    d5c373-client-ff81fb-c4rpr-xrcz2 itself is Ready=True now but the disk
    on it shows a DiskPressure/not-schedulable-for-more-replica condition
    (unrelated scheduling flag, not the cause of THIS volume's fault - this
    volume already has its one replica placed there). No second replica
    exists to fail over to (replicas=1 was the original design - no HA on
    this PVC).
    **NOT FIXED — needs explicit owner approval before I touch it.** The only
    paths to bring this volume back are salvage-with-possible-data-loss or
    delete+recreate the PVC (Grafana/kube-state-metrics pods on the same
    stack are fine, only the Prometheus TSDB volume is faulted) - both are
    destructive-recovery per CLAUDE.md hard rule ("no restore-from-backup,
    PVC deletion, force-detach ... without explicit approval"). Metrics data
    at risk is scrape history only (re-fills over time, no business data),
    but I am not authorizing that call myself. Flagging for owner: either (a)
    approve a Longhorn force-salvage/replica-delete-and-recreate on this PVC
    (accepts loss of existing TSDB history), or (b) someone restarts
    longhorn-manager on that node first to see if the salvage loop is a
    stuck-controller bug that clears on its own restart (lower risk, worth
    trying first, still asking because it's still a manual intervention on a
    live volume).
- not_ready_other (все известные, юзерские/unmaintained, без изменений):
  api-zerkalo-ru, ai-gateway-b72e67-dada-tuda-ru (unmaintained PublicApi,
  age ~ месяц+), nabeg-su и payments - age_seconds=7, это просто новые/свежие
  ресурсы в Pending на момент снимка, не поломка.
- domain_issues: m2-delwedge-6ccb0a, a2a-hub.pro (известные юзерские
  failed hostname/cert, с прошлых снимков) + bot.horizonmobile.ru
  stage=authorization status=pending age=3s - это старт ACME-челленджа,
  не failed.
- failed_builds (все юзерские, без изменений с прошлых циклов):
  dadadev-brains (no_dockerfile), smirad (framework_undetected),
  jkjk (framework_undetected) - все требуют юзерского Dockerfile/manifest,
  платформенных причин нет.

BLOCKED_USERS: none identified (stuck_operations=0, errors empty; live URL
checks in the snapshot show only the already-known gulyaev-ai-core 503,
no new blocked-by-platform signal)

СДЕЛАНО: диск-инцидент починил (docker cleanup, 300MiB -> 8.1GB free,
main build gate green again, verified live against Jenkins #157 SUCCESS +
clean local probe-main-build.sh run). Диагностировал (но НЕ трогал без
апрува) faulted Longhorn PVC под нашим kube-prometheus-stack - см. выше,
это платформенный P0/P1 но с деструктивным фиксом, жду решения владельца.

ПРОДУКТУ: без новых продуктовых симптомов с прошлого цикла (см. предыдущий
handoff 2026-09-16 для списка открытых bl: 0491/0492/0493/0495/0498/0499/
0501/0503/0505 - не проверял их состояние в этом цикле, это не мой P0 сейчас).
Единственный новый факт, который Automator должен знать: диск VM снова упал
до 300MiB (второй раз за последние ~10 дней, прошлый раз был 442MiB
2026-09-16) - паттерн повторяется, стоит завести автоматическую еженедельную
очистку dangling docker-образов в cron вместо ручной разборки каждый раз.
