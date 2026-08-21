---
id: 0271
status: closed
prio: P1
stream: 0
title: 2026-08-13 sess-0813g · P1-GOAL-NEVER-FIRED **ОТГРУЖЕНО 40755af0 origin/main, live-M2 в долге** · **Цель registration_complete не
created: 2026-08-13
sess: sess-0813g
section: 🎯 ПОТОК 0 — Empty-project activation (#1 ПО ДАННЫМ)
closed_at: 2026-08-13
closed_commit: 40755af0
---
- [x] 2026-08-13 sess-0813g · P1-GOAL-NEVER-FIRED **ОТГРУЖЕНО `40755af0` origin/main, live-M2 в долге** · **Цель `registration_complete` не сработала НИ РАЗУ за 14 дней** — ни в Metrika, ни в нашем зеркале `ux_events` (0 строк при 17 реальных регистрациях 08-08 и живой 08-11). Корень не вероятностный: маркер пишется как `"<timestamp>:<method>"` (`register-redirect.ts:100`), а `callback/page.tsx` читал его голым `Number(pending)` → `NaN` → ранний return перед `reachGoal`. Ломалось на КАЖДОЙ регистрации. Починка: разбор один на оба чтения — экспортируемая `readCompletedRegistration` рядом с `readAbandonedRegistration`; цель теперь несёт `{method}`. Гейты: tsc чисто, lint 0 errors, `test:unit` 202/202 (+2 новых), build прошёл. Red-proof: `Number("<ts>:yandex") = NaN`.
