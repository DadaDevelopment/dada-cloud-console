---
id: 0251
status: open
prio: P0
title: P1-BOX-CRYSTALLIZE-DEAD-ON-PROD · **Крышталлизация
sess: sess-0801
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🔴 P1-BOX-CRYSTALLIZE-DEAD-ON-PROD (sess-0801, найдено пробой аудита) · **Крышталлизация — шаг монетизации потока 6 (per-minute box → месячный VM-счёт) — на проде НЕ РАБОТАЕТ ВООБЩЕ.** Живой запрос отдал 503 [live: `POST /projects/640ed82d/boxes/m2-box-up-1/crystallize` с `ack_monthly_charge=false` → 503, строка аудита `1d647c68` {"reason":"local_runtime_unavailable","status":503}]. Причина [code `box_runtime.go:59-66`]: `requireLocalRuntime` отдаёт 503, если `s.local == nil`, а local-адаптер поднимается только при `BOX_LOCAL_ROOT` (`initBoxRuntime`, там же) — прод крутит кластерный адаптер, значит промоушен структурно недоступен. Продать бокс в VM нельзя. До `1bede15` этот отказ не писал в аудит НИЧЕГО, поэтому дыру не было видно ни в одной метрике. Решить: либо крышталлизация умеет кластерный адаптер, либо UI/MCP честно её не предлагают на проде.
