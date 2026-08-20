---
id: 0009
status: open
prio: P1
title: E43 ПЕРЕВЕДЁН В blocked-on-owner — ЭТО ВОПРОС К ВЛАДЕЛЬЦУ, А НЕ ЗАМЕР
created: 2026-08-19
sess: sess-0819b
section: Backlog (execution-bet)
---
- [ ] 🟠 E43 ПЕРЕВЕДЁН В `blocked-on-owner` — ЭТО ВОПРОС К ВЛАДЕЛЬЦУ, А НЕ ЗАМЕР (sess-0819b, 2026-08-19, [live psql+Metrika]). Четвёртый цикл подряд `payment_connections`=0 и `payment_oauth_states`=0 ever, `/accept-payments` 0 просмотров за 30д, `utm_source=accept_payments` нет в данных вообще. Причина проверена живьём как ОТСУТСТВУЮЩИЙ ФАКТ: partner-ключей YooKassa нет ни в одном секрете/env (грепом по всем неймспейсам), значит connect заблокирован по построению. Автоперепроверку снять — нового знания она не даст; нужен ключ от владельца. Фича отгружена и живая, рыночного сигнала по ней просто НЕ СУЩЕСТВУЕТ.
