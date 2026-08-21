---
id: 0329
status: open
prio: P0
stream: 6
title: P1-DELETEPROJECT-ОСТАВЛЯЕТ-ЖИВЫЕ-NAMESPACE · doDeleteProject в докстринге утверждает
created: 2026-08-09
sess: sess-0809
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🔴 P1-DELETEPROJECT-ОСТАВЛЯЕТ-ЖИВЫЕ-NAMESPACE (sess-0809 инцидент фарм-аккаунтов, 2026-08-09) · `doDeleteProject` [code gitops-agent/internal/worker/dbwatcher.go:1348-1406] в докстринге утверждает: «Namespace teardown … deliberately skipped: Argo/git-prune plus namespace finalizers reap the namespace(s)». Это НЕВЕРНО [live]. Namespace окружения рендерится чартом `project-defaults` с аннотацией `argocd.argoproj.io/sync-options: Prune=false` [argo-infra helm/project-defaults/templates/namespace.yaml:31]. `Prune=false` блокирует и prune, и каскадное удаление по `resources-finalizer.argocd.argoproj.io` (финализатор в шаблоне Application стоит). Итог: Application `project-defaults-<slug>` ApplicationSet удаляет, а namespace вместе со ВСЕМИ рабочими нагрузками остаётся жить вечно и невидимо для консоли — проект удалён, строк в БД нет, поды крутятся. Улика этого цикла: после удаления 20 проектов фарма остались `chenlikun-18-gmail-com-prod` (два пода `nodejs-argo*` в CrashLoopBackOff, живы через 20 мин после удаления проекта), `macmam-atomicmail-io-prod`, `ssa-prod`, `zengqcyxx-gmail-com-prod`; снесены руками `kubectl delete ns`. Это утечка денег (CPU/RAM крутятся без владельца) и безопасности (контейнеры забаненного юзера продолжают выполняться). · **Чинить НЕ снятием `Prune=false` глобально** — он стоит как защита от того, что временный сбой рендера генератора сотрёт namespace с живыми данными (класс: orphan-GC purge, stale-index clobber). Правильный путь — явное удаление namespace в `doDeleteProject` (нужен k8s-клиент в gitops-agent, которого сейчас нет) либо отдельный reaper по `dada.io/project`, сверяющийся с таблицей `projects` перед удалением. Решение с блэст-радиусом — вынести владельцу.
