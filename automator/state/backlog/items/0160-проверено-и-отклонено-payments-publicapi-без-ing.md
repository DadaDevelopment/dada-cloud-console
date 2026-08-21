---
id: 0160
status: open
prio: P0
title: ПРОВЕРЕНО И ОТКЛОНЕНО: «payments PublicApi без Ingress = P0, платежи отдают 404»
created: 2026-08-11
sess: sess-0811i
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟢 ПРОВЕРЕНО И ОТКЛОНЕНО (sess-0811i, 2026-08-11): «`payments` PublicApi без Ingress = P0, платежи отдают 404». Проверил сам: вебхук ЮKassa живёт на консольном API (`POST https://console.dada-tuda.ru/api/v1/webhooks/yookassa` → 400 на пустое тело, т.е. хендлер достигнут), а не на `payments.dada-tuda.ru`. Денежный тракт цел; строка `payments` в `not_ready_other` — мёртвый CR, не касса. Тот же ложный P0 поднимался прошлым циклом — если всплывёт третий раз, гасить ссылкой сюда.
