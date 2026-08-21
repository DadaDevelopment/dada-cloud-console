---
id: 0425
status: open
prio: P1
title: Удаление managed-базы не реклеймит: Orphan оставляет базу и роль на шарде
created: 2026-08-21
---
Прослежено по коду 2026-08-21 [code]: DeleteServiceDatabase (backend/internal/api/databases.go:616) -> операция -> gitops-agent doDeleteServiceDatabase (worker/dbwatcher.go:931) -> запись убирается из manifests: (worker/resources_values.go:88), коммит+пуш, DELETE FROM resource_snapshots (dbwatcher.go:989). Argo прунит CR.

Дальше РАЗРЫВ: у Crossplane Database и Role внутри композиции ServiceDatabaseV2 стоит deletionPolicy: Orphan (tasks/postgres-multitenancy-design.md:545, gitops-agent/internal/worker/move_app_db.go:14-17, ADR-014:115, runbooks/stateful-app-move.md:27). DROP DATABASE / DROP OWNED / DROP ROLE не делает ни backend, ни gitops-agent — ноль вхождений в прод-коде. Итог: строка пропадает, квота освобождается, база и роль живут на платном шарде, видны только в /admin/db-shards orphan=true. Прошлые уборки — руками (postgres-multitenancy-design.md:447-451, :578-582; goal-db-insights.md:146).

Готовый образец фикса лежит в этом же репо: deleteProjectKeycloakGroups (gitops-agent/internal/worker/delete_project_keycloak.go:31-51,147) флипает deletionPolicy на Delete ЧЕРЕЗ GIT (patch на живой CR selfHeal откатывает за ~2 мин), ждёт доставки до живых CR (4 мин), и только потом сносит манифест — тогда прун доходит до внешнего состояния.

Что нужно: (1) в композиции ServiceDatabaseV2 (argo-infra/dada-argo, вне этого репо) дать способ включить Delete на пути удаления, не трогая Orphan в обычной жизни — на нём держится data-safe re-point при MoveApp; (2) в doDeleteServiceDatabase повторить двухшаговый паттерн Keycloak; (3) прогон на agent-sandbox с пруфом отсутствия базы и роли на шарде. Требует сети до кластера и правки соседнего репо — блокировано, пока машина слепа.

Пока разрыва нет — текст в консоли не обещает стирания (22decded), это заплатка честности, а не реклейм.

Побочный долг: снять tlsprobe (agent-sandbox/prod, id 64845488-e2ad-4cc9-8522-7ffd6d352fae) — DROP DATABASE + DROP OWNED + DROP ROLE svc-tlsprobe.
