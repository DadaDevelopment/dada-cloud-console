# First-use sweep — 10 issues (2026-07-14)

User's first 5 min on prod console. Fix ALL, verify on prod.

| # | Issue | Source | Owner | Status |
|---|-------|--------|-------|--------|
| 1 | "IAM resource not found" on Участники (members) | screenshot | investigator E / bg task | OPEN |
| 2 | redis dup error leaks project "platform" + global uniqueness (S3-bucket surprise) | screenshot+msg | me | DONE (1674e06 leak, 5c850ee per-project) |
| 3 | create-app validation UX: error only after submit, no inline validation | screenshot | me (frontend) | OPEN |
| 4 | Operations page visible to user ("я просил убрать") | screenshot | investigator A | OPEN |
| 5 | Config card leaks "common/values.yaml"; Cost card "фактический расход ресурсов кластера" (k8s/cluster ban-words); create-app label "Имя (имя ресурса Kubernetes)" | screenshot | me (frontend) | OPEN |
| 6/7 | env-vars panel untranslated English while UI is RU | screenshot | me (i18n) | OPEN |
| 8 | "Не удалось загрузить стоимость" cost load failed | screenshot | investigator B | OPEN |
| 9 | Domain rendered `myredis-c1e9e9-dada-tuda-ru` (dashes not dots) | screenshot | investigator C | OPEN |
| 10 | myredis stuck "Processing" 5+ min, turtle speed | msg | investigator D | OPEN |

Root cause + prod verification required per item.
