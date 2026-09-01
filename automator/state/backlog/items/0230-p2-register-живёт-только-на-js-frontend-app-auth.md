---
id: 0230
status: open
prio: P2
title: P2-/register-ЖИВЁТ-ТОЛЬКО-НА-JS · frontend/app/(auth)/register/page.tsx отдаёт 200 с телом из одного спиннера и в useEffect дергае
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟡 P2-`/register`-ЖИВЁТ-ТОЛЬКО-НА-JS · `frontend/app/(auth)/register/page.tsx` отдаёт 200 с телом из одного спиннера и в `useEffect` дергает `startRegister()` (`frontend/lib/register-redirect.ts`) → `signinRedirect({prompt:"create"})`. Ни `<noscript>`, ни обычной ссылки на Keycloak в разметке нет. Медленный бандл, режущее расширение или отключённый JS = человек навсегда остаётся на «Открываем регистрацию…». Живых потерь не замерено (замерить нечем, см. пункт выше), но отказ жёсткий и запасного пути нет. Чинится в нашем репозитории одной ссылкой в `<noscript>`.
