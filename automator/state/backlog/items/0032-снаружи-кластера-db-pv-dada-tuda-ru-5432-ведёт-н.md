---
id: 0032
status: open
prio: P1
title: СНАРУЖИ КЛАСТЕРА db.pv.dada-tuda.ru:5432 ВЕДЁТ НЕ ТУДА
created: 2026-08-15
sess: sess-0815r
section: Backlog (execution-bet)
---
- [ ] 🟠 СНАРУЖИ КЛАСТЕРА `db.pv.dada-tuda.ru:5432` ВЕДЁТ НЕ ТУДА (sess-0815r, 2026-08-15, [live: dig + kubectl configmap], origin/main@7990299d) — публичный DNS отдаёт 155.212.223.198 (`network/ingress-nginx-pub-controller`), а его `ingress-nginx-pub-tcp` мапит `"5432": databases/postgresql:5432` — старый общий Postgres, plaintext, без роли юзера. Внутри кластера строка живёт только за счёт `hostAliases`. Значит DSN со страницы базы принципиально не работает с ноутбука юзера (`psql`, DBeaver, миграции с локальной машины), и это не сказано нигде. Либо публичный 5432 маршрутизировать в `pg-router`, либо честно подписать строку как внутрикластерную. Решение про публичный LB — инфраструктурное, вынести владельцу.
