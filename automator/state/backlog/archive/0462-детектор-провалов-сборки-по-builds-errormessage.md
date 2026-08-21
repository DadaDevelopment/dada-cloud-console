---
id: 0462
status: closed
prio: P1
stream: 3
title: Детектор провалов сборки по builds.error_message структурно слеп: текст ошибки живёт только в builds_logs
created: 2026-08-21
sess: sess-0822b
closed_at: 2026-08-21
closed_commit: 3d23f00c
closed_note: error_message теперь несёт причину, а не префикс npm; новый код missing_manifest снимает счёт с юзерского Dockerfile; фикстура = дословная консоль #447, RED на старом коде
---
Замер [live psql, 90д, sess-0822b]: запрос по `builds.error_message ILIKE '%package.json%ENOENT%'`
возвращает 0 строк, хотя класс существует и стоил живого юзера. Причина: в `error_message`
приезжает только хвост npm-лога («A complete log of this run can be found in …»), а сама строка
`npm error enoent Could not read package.json` лежит только в `builds_logs.line`.

Следствие: любой дашборд/алерт/беклог-запрос, построенный на `builds.error_message`, будет вечно
рапортовать ноль инцидентов по этому классу.

Что делать:
1. build-agent должен писать в `error_message` ту строку, которую `pickCause` уже выбрал из
   BuildKit-конверта (`build-agent/internal/worker/runner.go:495` pickCause / buildkitExcerpt),
   а не хвост лога.
2. Отдельный `fail_reason` для «манифеста нет» (npm без package.json, pip без
   requirements/pyproject): сейчас это называется `dockerfile_build_failed`, хотя Dockerfile
   сгенерировали МЫ, а не юзер — счёт выставлен не тому.
3. Приёмка: SELECT по `error_message` находит те же строки, что и по `builds_logs`.
