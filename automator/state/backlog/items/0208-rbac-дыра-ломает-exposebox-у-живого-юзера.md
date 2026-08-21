---
id: 0208
status: open
prio: P1
title: RBAC-ДЫРА ЛОМАЕТ ExposeBox У ЖИВОГО ЮЗЕРА
sess: sess-0806o
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟠 RBAC-ДЫРА ЛОМАЕТ ExposeBox У ЖИВОГО ЮЗЕРА (sess-0806o, разбор аудита, [live+code]) — `ExposeBox` отдаёт 500: сервис-аккаунту `argocd-prod:dada-cloud-console` не хватает прав на `configmaps` в ns `dada-boxes`, куда `backend/internal/box/clusterexpose.go:295` пишет конфиг `box-noindex-headers` (константа `clusterexpose.go:59`). Правка готовая — Role/RoleBinding в чарте консоли. Проверить, что это не единственная недостающая пермиссия в этом ns.
