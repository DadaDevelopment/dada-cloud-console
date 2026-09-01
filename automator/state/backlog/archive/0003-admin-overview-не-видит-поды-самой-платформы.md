---
id: 0003
status: closed
prio: P0
title: /admin/overview НЕ ВИДИТ ПОДЫ САМОЙ ПЛАТФОРМЫ
created: 2026-08-19
sess: sess-0819a
section: Backlog (execution-bet)
closed_at: 2026-08-19
closed_commit: f44dd84d
closed_note: Секция platform_health в /api/v1/admin/overview: поды+workloads платформенных namespace (PLATFORM_HEALTH_NAMESPACES, деф. argocd-prod) читаются напрямую из k8s, три состояния вместо двух (observed=false + unavailable_reason при слепоте), фронт красным пишет что пустота != здоровье. 7 go-тестов на fake clientset + 6 фронтовых. ДОЛГ: живой пруф (крашлупящийся платформенный под в панели) не снят — с машины нет TCP до прода (item 0374).
---
- [ ] 🔴 `/admin/overview` НЕ ВИДИТ ПОДЫ САМОЙ ПЛАТФОРМЫ (sess-0819a, 2026-08-19, [live kubectl vs панель]). Панель, по которой владелец судит о здоровье, перечисляет только аппы юзеров. Крашлупящий или потерявший git-доступ `gitops-agent` — то есть мёртвая доставка ВСЕЙ платформы — в ней не появляется вообще. Сегодня это поймали глазами в логах, а не панелью.
