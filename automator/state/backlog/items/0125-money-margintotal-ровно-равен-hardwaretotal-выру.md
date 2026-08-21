---
id: 0125
status: open
prio: P2
title: money.margin_total РОВНО РАВЕН -hardware_total, ВЫРУЧКА НЕ ВЫЧТЕНА
created: 2026-08-13
sess: sess-0813a
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟡 `money.margin_total` РОВНО РАВЕН `-hardware_total`, ВЫРУЧКА НЕ ВЫЧТЕНА (sess-0813a, 2026-08-13, [live /admin/overview]) — в ответе `margin_total=-13194` при `paid_total=990` и `metered_total=2848.36`. Похоже, расчёт маржи не учитывает выручку вообще. Не расследовано, помечено `unmeasured`. Прямо бьёт в правило владельца «числа админки обязаны нести бизнес-смысл» ([[feedback_admin_numbers_business_meaning]]).
