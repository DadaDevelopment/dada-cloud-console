---
id: 0250
status: open
prio: P2
title: P2-BOX-DELETING-STALE-ROW · m2-boxlive-0801b висит Deleting с 2026-07-31T21:34Z (>11ч), пода в dada-boxes уже НЕТ
created: 2026-07-31
sess: sess-0801k
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟡 P2-BOX-DELETING-STALE-ROW (sess-0801k, побочно из burst-M2) · `m2-boxlive-0801b` висит `Deleting` с 2026-07-31T21:34Z (>11ч), пода в `dada-boxes` уже НЕТ [live] — то есть строка в БД пережила инфру и воркер операций её не дожал. Рядом `m2-boxlive-0801` в `Failed: pool_exhausted` (до фикса). Наши собственные боксы этого цикла удалились чисто (под+PVC ушли, квота вернулась к `pods 1/6, 20Gi/120Gi` [live]), значит путь удаления в целом жив, а это — залипшие строки, которые к тому же держат ИМЯ (см. P2-BOX-FAILED-NAME-STUCK).
