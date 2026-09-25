---
id: 0522
status: open
prio: P1
stream: 2
hypothesis: H02
title: Терминальное audit-действие врёт про успех: CrashLoop-апп неотличим от живого в разборе пути
created: 2026-09-25
sess: sess-0925a
---
[live psql 2026-09-25] Двое из 30-дневной когорты (artem4212, wow83168) имеют терминальное
audit-действие со статусом success (CreateApp/success, ResolveAutofix/success), а их аппы
прямо сейчас в CrashLoop (app_health_alerts cause_kind='app_code', оба last_send_ok=t).

То есть ритуал разбора аудита (постоянное правило SKILL) систематически считает их
активированными. Знаменатель "активация" завышен минимум на 2 из 15 за окно.

Чинить: audit-разбор обязан джойнить терминальное действие с живым phase
(resource_snapshots.summary_json->>'phase') и app_health_alerts по app_name. Либо
завести audit-action при переходе аппа в CrashLoop, чтобы путь юзера содержал падение,
а не только его собственные клики.

Связано: 0507 (честный статус для аппов без HTTP) - но это другой класс, там апп жив.
