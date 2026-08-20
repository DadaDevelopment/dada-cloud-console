---
id: 0323
status: open
prio: P2
stream: 6
title: P2-НАШ-REELS-TRACKER-БЕЗ-ENV · platform-prod/reels-tracker в CrashLoopBackOff 4ч45м
sess: sess-0803b
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🟡 P2-НАШ-REELS-TRACKER-БЕЗ-ENV (sess-0803b, из пульса) · `platform-prod/reels-tracker` в CrashLoopBackOff 4ч45м [live]: `pydantic_core.ValidationError: telegram_bot_token Field required`. Это НАШ сервис, не клиентский, но симптом ровно тот, ради которого делается P1-CRASH-CAUSE-NOT-STORED: причина есть в логе и нигде больше. Годится как живая мишень для проверки того фикса, когда он доедет. Отдельно: 6 из 10 упавших билдов за 48ч — `git_auth_failed` на нашем же platform-репо (`could not read Username for 'https://github.com'`), повторяется. Клиентских упавших билдов за 48ч — 0.
