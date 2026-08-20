---
id: 0349
status: open
prio: P2
stream: 6
title: СЕРТ a2a-hub.pro НЕ ВЫПИСАН 9 СУТОК — domain_issues, причина в CR: «Issuing certificate as Secret does not exist»
created: 2026-08-11
sess: sess-0811d
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🟡 СЕРТ `a2a-hub.pro` НЕ ВЫПИСАН 9 СУТОК (sess-0811d, 2026-08-11, [live /api/v1/admin/overview + kubectl]) — `domain_issues`, причина в CR: «Issuing certificate as Secret does not exist». Девять суток в одном состоянии = механизм, а не сетевая заминка. Не мой як в этом цикле (взят `not_ready_other`), но это единственная строка `domain_issues`, которая означает реально недоступный юзерский хост.
