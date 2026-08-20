---
id: 0002
status: closed
prio: P0
title: b2a9a842 (проверено git show: аннотация checksum/secret на обоих pod-template + git-tracked existingSecretVersion, т.к
created: 2026-08-19
sess: sess-0819c
section: Backlog (execution-bet)
closed_at: 2026-08-19
closed_commit: b2a9a842
---
- [x] ЗАКРЫТ sess-0819c 2026-08-19 · `b2a9a842` (проверено `git show`: аннотация `checksum/secret` на обоих pod-template + git-tracked `existingSecretVersion`, т.к. живой секрет в проде не рендерится чартом) · 🔴 РЕДАКТИРОВАНИЕ СЕКРЕТА НЕ КАТИТ ПОДЫ — ПЛОХОЕ ЗНАЧЕНИЕ ЖИВЁТ ДО СЛУЧАЙНОГО РЕСТАРТА (sess-0819a, 2026-08-19, [code] `helm/dada-cloud-console/templates/backend-deployment.yaml:44-46`, `gitops-agent-deployment.yaml:41-42`, origin/main@17db736d). Оба шаблона берут env через `envFrom.secretRef` и НЕ несут `checksum/secret` на pod-template. Значение фиксируется на старте контейнера, поэтому правка секрета (хорошая или плохая) не доезжает, пока под не перезапустят по другой причине. Именно так испорченный ключ прожил ~21 час, и ровно так же будет жить починка, если её кто-то применит без рестарта. Чинить: аннотация с хэшем содержимого на обоих шаблонах; сперва выяснить, кто вообще пишет `dada-cloud-console-backend` (в `values.yaml` только плейсхолдеры, живой секрет — ручной `kubectl patch`).
