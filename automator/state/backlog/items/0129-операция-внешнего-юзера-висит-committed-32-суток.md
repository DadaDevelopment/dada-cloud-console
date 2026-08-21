---
id: 0129
status: open
prio: P1
title: ОПЕРАЦИЯ ВНЕШНЕГО ЮЗЕРА ВИСИТ Committed 32 СУТОК, И stuck_operations ЕЁ НЕ ВИДИТ
created: 2026-08-12
sess: sess-0812k
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟠 ОПЕРАЦИЯ ВНЕШНЕГО ЮЗЕРА ВИСИТ `Committed` 32 СУТОК, И `stuck_operations` ЕЁ НЕ ВИДИТ (sess-0812k, 2026-08-12, [live /admin/overview + psql + kubectl], origin/main@ea564d44) — `api-zerkalo-ru` (sergeykozlov2006@gmail.com): три `CreatePublicApi`, все `Committed`, ноль строк `git_repos`, ноль объектов в кластере, CR нет (`NotFound`). То есть внешний человек месяц назад создал публичный API и не получил НИЧЕГО, а панель поломок держит это в `not_ready_other`, но гейт `stuck_operations` молчит: у состояния `Committed` для PublicApi нет таймаута. Рычаг: терминальный таймаут на `Committed` без появившегося ресурса (как у операций аппов), иначе такие строки и дальше уходят в тишину.
