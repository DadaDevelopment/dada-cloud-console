---
id: 0167
status: open
prio: P0
title: 3 PublicApi CRD висят в crossplane reconcile 6.7–19.8 суток (n8n, svod, api-zerkalo-ru) при том, что сами App — Ready и свежие
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 3 PublicApi CRD висят в crossplane reconcile 6.7–19.8 суток (`n8n`, `svod`, `api-zerkalo-ru`) при том, что сами App — `Ready` и свежие. Панель поломок их не считает: предикат головного счётчика — только `kind='App'`, CRD-залипания падают в `not_ready_other` и никого не будят.
