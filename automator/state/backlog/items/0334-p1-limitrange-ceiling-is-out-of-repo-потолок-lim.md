---
id: 0334
status: open
prio: P1
stream: 6
title: P1-LIMITRANGE-CEILING-IS-OUT-OF-REPO: потолок LimitRange Container max cpu=4/mem=2Gi рендерится ArgoCD-приложением project-default
sess: sess-0810e
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] P1-LIMITRANGE-CEILING-IS-OUT-OF-REPO (sess-0810e, из аварии fonbet-value): потолок `LimitRange Container max cpu=4/mem=2Gi` рендерится ArgoCD-приложением `project-defaults-<slug>`, чей исходник лежит ВНЕ `dada-cloud` и вне доступного kubectl-контекста. Код теперь этот потолок соблюдает (коммит `87e7f37e`), но НЕ назначает. Вопрос владельцу: 2Gi — это осознанная платформенная политика или протухший дефолт? Пока он ниже `appAutoscaleMaxMemoryLimit=16Gi`, автоскейлер обещает рост, которого платформа не даст. Живой кейс: fonbet-value реально просил ~4Gi, получит 2Gi.
