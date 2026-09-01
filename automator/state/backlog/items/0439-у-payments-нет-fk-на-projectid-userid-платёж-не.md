---
id: 0439
status: open
prio: P2
title: У payments нет FK на project_id/user_id -- платёж не сводится с юзером иначе как по тексту org_id
created: 2026-08-21
sess: sess-0821h
---
Заземление sess-0821h [code+live]. У таблицы `payments` нет FK ни на `project_id`, ни на
`user_id` -- есть только текстовый общий `org_id`.

Следствие: платёж нельзя достоверно свести с конкретным юзером и его проектом иначе как
разбором текста. Это ровно тот класс, что уже дважды бил по деньгам
(см. `project_checkout_persists_pending_row_without_creating_payment.md`,
`project_checkout_recorded_outcome_through_payers_own_context.md`).

Пока внешних `succeeded` платежей 0 за 30 дней -- это дешёвая правка. После первого потока
платежей она станет миграцией с бэкфиллом по тексту.
