---
id: 0185
status: open
prio: P0
title: ПОТОК-2 ВОССТАНОВИТЬ ИНВЕНТАРЬ platform ПОСЛЕ ЛОЖНОГО PURGE
created: 2026-08-08
sess: sess-0808e
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🔴 ПОТОК-2 ВОССТАНОВИТЬ ИНВЕНТАРЬ `platform` ПОСЛЕ ЛОЖНОГО PURGE (sess-0808e, 2026-08-08) — фикс `72ee73d5` останавливает течь, но ~40 уже удалённых строк (`jenkins`, `nexus`, `portainer`, `neo4j`, `jira`, `rabbitmq`, `postgres-shard-0`, `opensearch`, `grafana`, `kube-prometheus-stack`, `kserve*`, `knative*`, `opencost`, `fluent-bit`, `elastic-stack`, `mlflow*`, `pgadmin`, `postgresql`) сами не вернутся, если снапшот рождается только рендером из БД. Заземлить: `discover()` в `statusreconciler.go` и флаг `ClusterDiscoveryEnabled` в проде — воссоздаёт ли он строку по живому workload. Не воссоздаёт → написать разовый бэкфилл. Проверка: в консоли проекта `platform` снова видны jenkins/nexus/portainer/neo4j.
