---
id: 0464
status: open
prio: P2
stream: 3
title: Незакрытые ретрай-петли юзера: VerifyDomainAuthorization 31x/5мин и RevealEnvVar 23x/4ч18м
created: 2026-08-21
sess: sess-0822b
---
Замер [live audit_events, sess-0822b]: у kkartov помимо известных 172x `SeedDatabaseDSN` найдены
ещё две петли — 31 отказ `VerifyDomainAuthorization` за 5 минут и 23 отказа `RevealEnvVar`
за 4ч18м. Ни одна не поймана синхронным стражем.

Нужен общий детектор: N однотипных failure одного актора за окно -> операция получает терминальный
вердикт, а причина показывается юзеру В ПРОДУКТЕ (письма юзерам запрещены). Смежно с 0461.
