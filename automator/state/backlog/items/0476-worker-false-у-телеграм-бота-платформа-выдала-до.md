---
id: 0476
status: open
prio: P2
stream: 2
title: worker=false у телеграм-бота: платформа выдала домен+ingress аппу без HTTP-листенера
created: 2026-08-22
sess: sess-0822f
---
sevarateambot (bruzas.85@mail.ru): воркер-бот на pytelebot, HTTP-листенера на 8000 нет и не будет. Платформа всё равно создала домен, ingress и service, поэтому 502 постоянный по дизайну, а панель ломаемостей числит апп dead_app — ложный позитив.

Нужен: (а) определение воркера (нет открытого порта в течение N минут после Ready -> worker=true), (б) для worker=true не заводить домен/ingress и не мерить http_status, (в) рычаг для юзера переключить тип аппа вручную.
