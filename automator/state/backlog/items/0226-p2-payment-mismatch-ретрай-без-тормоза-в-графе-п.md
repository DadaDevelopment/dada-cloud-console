---
id: 0226
status: open
prio: P2
title: P2-PAYMENT-MISMATCH-РЕТРАЙ-БЕЗ-ТОРМОЗА · В графе переходов PaymentPlanMismatchDetected → PaymentPlanMismatchDetected 13 повторов п
sess: sess-0806d
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟡 P2-PAYMENT-MISMATCH-РЕТРАЙ-БЕЗ-ТОРМОЗА (sess-0806d, побочно из разбора аудита, НЕ брал по правилу одного яка) · В графе переходов `PaymentPlanMismatchDetected → PaymentPlanMismatchDetected` 13 повторов подряд у одного юзера без единого внешнего действия между ними [live psql, audit_events]. Похоже на ретрай без backoff и без дедупликации: одно расхождение плана пишет 13 строк аудита.
      ⚠️ ГИПОТЕЗА «РЕТРАЙ БЕЗ BACKOFF» ОПРОВЕРГНУТА [code sess-0806e]: дедуп есть — `paymentMismatchDedupWindow` (24ч) в `backend/internal/api/billing_mismatch.go`. Настоящий механизм: гейт держится в `auditSeen` — обычной in-memory мапе процесса, а бэкенд крутится в `replicas: 2`. Две реплики ведут ДВЕ независимые мапы, и каждый рестарт/раскатка обнуляет обе. То есть 24-часовое окно не переживает ни второй под, ни деплой. Чинить не backoff'ом, а тем, что состояние дедупа должно быть общим (строка в БД / advisory-запрос по последнему аудиту), — а не памятью пода.
