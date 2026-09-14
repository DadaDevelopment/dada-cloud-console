---
id: 0503
status: open
prio: P1
stream: 2
hypothesis: H11
title: archive_port клобберит git_repos.port у upload-аппов: та же дыра для start_command/replicas
created: 2026-09-14
sess: sess-0914a
---
Найдено при отгрузке 0502 (ff847fac), заведено как отдельный пункт по правилу одного яка.

sourceForBuild накладывает на строку аппа значения с билда [code build-agent/internal/worker/runner.go:1879-1896]: archive_framework -> FrameworkOverride, archive_port -> Port (только если >0). Это значит, что для archive-аппа git_repos НЕ является авторитетным источником, пока есть незавершённый билд: любая правка поля, которое дублируется на builds, проигрывает уже летящему билду.

В 0502 это закрыто ТОЧЕЧНО для port: SetUploadPort пишет обе строки в одной транзакции [code backend/internal/api/uploadport.go, UPDATE builds ... WHERE status IN ('queued','detecting','building','pushing')].

Незакрытое: тот же класс для остальных полей оверлея. Проверить, какие ещё правки юзера (start_command, replicas, profile, framework_override) могут быть перезаписаны летящим archive-билдом, и лечить механизмом (оверлей читает актуальную строку либо мержит только поля билда), а не ещё одной точечной транзакцией. Родственный класс full-object LWW описан в 0501 (deployImageVersion + UpsertSnapshot).

Приоритет по данным: upload-путь живой (25 upload-билдов за 14д на момент 09-11), но конкретно эта гонка требует правки ВО ВРЕМЯ билда - событие редкое. Не брать вперёд измеренного узкого места.
