---
id: 0166
status: open
prio: P0
title: git_repos.port расходится с реально задеплоенным containerPort (замерено на sevarateambot ×2 и oxygen)
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] `git_repos.port` расходится с реально задеплоенным containerPort (замерено на sevarateambot ×2 и oxygen). Аварии не даёт — Service targetPort тянется за Deployment, не за колонкой — но колонке нельзя верить как источнику правды в будущих разборах.
