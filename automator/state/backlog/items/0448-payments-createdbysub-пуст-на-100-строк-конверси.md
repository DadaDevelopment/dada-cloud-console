---
id: 0448
status: open
prio: P2
title: payments.created_by_sub пуст на 100% строк — конверсию чужих плательщиков измерить нечем
created: 2026-08-21
sess: sess-0821f
---
Аудит sess-0821f: все 5 платежей за 7д — без `created_by_sub`. Отличить оплату
владельца org от чужой можно только эвристикой `org_id`/`customer_email`
(ненадёжна, см. memory `project_checkout_recorded_outcome_through_payers_own_context`).
За всё время в базе 1 `succeeded` — тоже своя org. Гейт «за что платить» мерить
нечем, пока колонка реально не заполняется на чекауте.
