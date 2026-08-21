---
id: 0192
status: open
prio: P2
title: ВСПЛЕСК Missing parameter: code_challenge_method
created: 2026-08-07
sess: sess-0807n
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟡 ВСПЛЕСК `Missing parameter: code_challenge_method` — ПРОИСХОЖДЕНИЕ НЕ УСТАНОВЛЕНО (sess-0807n, 2026-08-07, [live kcadm events]) — ~10 событий `LOGIN_ERROR` на клиенте `dada-console` за 40 секунд 2026-08-06 07:14Z, redirect_uri `console.dada-tuda.ru/callback`, IP смешанные (внешний 155.212.223.198 и кластерные). Клиентские `attributes` сейчас `{}`, то есть PKCE не вынуждается — значит в тот момент вынуждался либо звал кто-то без PKCE. Может быть нашими же curl-пробами из параллельной сессии — НЕ строить на этом выводов, сначала развести свои пробы от чужих по IP.
