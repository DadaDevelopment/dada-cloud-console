---
id: 0285
status: open
prio: P2
title: ARGOCD SELFHEAL ВЕРНЁТ replicas=1 ПОД РУКАМИ ЧЕРЕЗ ~2 МИН
created: 2026-08-12
sess: sess-0812a
section: Открытые долги (не терять)
---
- [ ] 🟡 ARGOCD SELFHEAL ВЕРНЁТ `replicas=1` ПОД РУКАМИ ЧЕРЕЗ ~2 МИН (sess-0812a, 2026-08-12, [live]) — при разборе клина `scale deploy jenkins --replicas=0` был молча отыгран назад, и следующий шаг (debug-под с тем же RWO-томом) упал в `Multi-Attach error`. Это не новость (`project_argocd_selfheal_reverts_live_spec_patch`), но в рунбуках аварийного обслуживания нигде не написано: любой ремонт, требующий отцепить том, начинается с приостановки Argo-приложения, иначе оно воюет с тобой.
