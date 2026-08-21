---
id: 0334
status: closed
prio: P1
stream: 6
title: P1-LIMITRANGE-CEILING-IS-OUT-OF-REPO: потолок LimitRange Container max cpu=4/mem=2Gi рендерится ArgoCD-приложением project-default
sess: sess-0810e
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
closed_at: 2026-08-21
closed_note: Закрыт: LimitRange.max.memory поднят 2Gi -> 4Gi в argo-infra helm/project-defaults/values.yaml, коммит b8b42b23 на ветке console-migration (main этого чарта не содержит вовсе -- источник найден: helm/project-defaults/templates/limitrange.yaml + values.yaml:18, per-project override через defaults.limitRange в clusters/beget-prod/projects/<slug>/project.yaml). 2Gi совпадал с лимитом профиля large (1Gi/2Gi), с которого аппы стартуют -- вся лестница по памяти была недостижима у ВСЕХ тенантов. 4Gi выбран ниже resourceQuota.limitsMemory=12Gi и requestsMemory=6Gi, чтобы реальным бюджетом осталась квота. Рендер проверен helm template: max.memory=4Gi при сохранённом per-project maxStorage=20Gi.
---
- [ ] P1-LIMITRANGE-CEILING-IS-OUT-OF-REPO (sess-0810e, из аварии fonbet-value): потолок `LimitRange Container max cpu=4/mem=2Gi` рендерится ArgoCD-приложением `project-defaults-<slug>`, чей исходник лежит ВНЕ `dada-cloud` и вне доступного kubectl-контекста. Код теперь этот потолок соблюдает (коммит `87e7f37e`), но НЕ назначает. Вопрос владельцу: 2Gi — это осознанная платформенная политика или протухший дефолт? Пока он ниже `appAutoscaleMaxMemoryLimit=16Gi`, автоскейлер обещает рост, которого платформа не даст. Живой кейс: fonbet-value реально просил ~4Gi, получит 2Gi.
