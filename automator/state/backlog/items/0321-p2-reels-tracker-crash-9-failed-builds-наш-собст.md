---
id: 0321
status: open
prio: P2
stream: 6
title: P2-REELS-TRACKER-CRASH-9-FAILED-BUILDS · Наш собственный сервис reels-tracker (ns platform-prod) в CrashLoopBackOff двумя подами (
sess: sess-0803f
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🟡 P2-REELS-TRACKER-CRASH-9-FAILED-BUILDS (sess-0803f пульс, [live]) · Наш собственный сервис `reels-tracker` (ns `platform-prod`) в CrashLoopBackOff двумя подами (exit=1) плюс 9 упавших сборок за 11 дней — столько же, сколько у самого проблемного клиентского бота. В experiments.md и в памяти не задокументирован (в `project_reels_instagram_egress.md` был ДРУГОЙ симптом — SNI-throttle, не CrashLoop). Клиент не задет, поэтому не P1. Разобрать root-cause отдельным циклом, не откладывать в общий шум.
