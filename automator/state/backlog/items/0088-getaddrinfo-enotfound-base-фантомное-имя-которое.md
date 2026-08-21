---
id: 0088
status: open
prio: P1
hypothesis: H08
title: getaddrinfo ENOTFOUND base — ФАНТОМНОЕ ИМЯ, КОТОРОЕ ЮЗЕР НИКОГДА НЕ ПИСАЛ, И АССИСТЕНТ ПОВТОРИЛ ЕГО КАК ДИАГНОЗ — pg-connection-st
created: 2026-08-14
sess: sess-0814c
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟠 `getaddrinfo ENOTFOUND base` — ФАНТОМНОЕ ИМЯ, КОТОРОЕ ЮЗЕР НИКОГДА НЕ ПИСАЛ, И АССИСТЕНТ ПОВТОРИЛ ЕГО КАК ДИАГНОЗ (sess-0814c, 2026-08-14, [live logs + `agent_chat_messages` session 417a9012], hypothesis: H08) — `pg-connection-string` парсит строку без схемы как relative URL против dummy-базы `postgres://base`: вставленный хост уезжает в поле `database`, а хостом становится литерал `base`. Юзер `artempro2022` видел в логах имя, которого нет нигде в его конфиге, и ассистент в 23:16Z выдал ему «проверьте `DB_HOST`/`DB_USER`/`DB_PASS`», а в 23:27Z «убедитесь что `DB_NAME`=megafactory» — таких переменных в его аппе НЕТ, апп читает только `DATABASE_URL`. ДЕЙСТВИЕ: (1) `cause_kind` на сигнатуру `ENOTFOUND`/`ECONNREFUSED` в логах + сверка с реальным значением `DATABASE_URL` аппа и managed-БД проекта → человеческий вердикт «твой `DATABASE_URL` — это только хост, вот полная строка» (`backend/internal/notify/notify.go:326` знает `app_needs_args` и соседей); (2) ассистент обязан ПОКАЗАТЬ значение env, а не гадать по логу.
