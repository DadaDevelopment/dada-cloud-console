---
id: 0253
status: open
prio: P2
title: P2-BOX-STUCK-DELETING · Box 2fc53734-ea51-40cb-afba-70301cdefdb6 висит в статусе Deleting с 2026-07-31T21:34:12Z
created: 2026-07-31
sess: sess-0801g
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟡 P2-BOX-STUCK-DELETING (sess-0801g, пульс) · Box `2fc53734-ea51-40cb-afba-70301cdefdb6` висит в статусе `Deleting` с 2026-07-31T21:34:12Z — на момент пульса ~1ч36м без смены состояния [live psql]. Остальной флот здоров (pods 2/6, storage 40Gi/120Gi, ноль строк в Requested/Created). Та же семья, что 5 сегодняшних box-фиксов на main — НЕ брал в этом цикле, box активно правит параллельная сессия. Проверить: это настоящий вис или незавершённая операция воркера.
