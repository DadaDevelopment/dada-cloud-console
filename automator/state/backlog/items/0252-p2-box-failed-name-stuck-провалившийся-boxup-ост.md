---
id: 0252
status: open
prio: P2
title: P2-BOX-FAILED-NAME-STUCK · Провалившийся boxUp оставляет строку box в Failed И её environment, поэтому повтор с ТЕМ ЖЕ именем полу
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟡 P2-BOX-FAILED-NAME-STUCK · Провалившийся `boxUp` оставляет строку box в `Failed` И её environment, поэтому повтор с ТЕМ ЖЕ именем получает `an environment with that name already exists in this project` [live: `m2-boxlive-0801`]. Для агента, который ретраит по тому же имени, это второй отказ подряд на ровном месте. Либо провал чистит за собой environment, либо retry с тем же именем переиспользует брошенную строку.
