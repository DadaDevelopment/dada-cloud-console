---
id: 0126
status: open
prio: P0
title: ПОПРАВКА ВОЗРАСТА: api-zerkalo-ru висит 22.3 суток, НЕ 32 — сверено по staleness resource_snapshots (не обновлялась с 07-21)
created: 2026-08-13
sess: sess-0813a
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟢 ПОПРАВКА ВОЗРАСТА: `api-zerkalo-ru` висит 22.3 суток, НЕ 32 (sess-0813a, 2026-08-13, [live]) — сверено по staleness `resource_snapshots` (не обновлялась с 07-21). Апп юзера `magic-mirror` (`ggrk52`) при этом жив и здоров: 2/2 Running 21д, свои ingress (`ggrk52.ru`, `magic-mirror-7679ef.dada-tuda.ru`) работают. То есть это брошенная попытка кастомного домена, а не поломка живого аппа. Пункт про отсутствие таймаута у `Committed` для PublicApi остаётся в силе.
