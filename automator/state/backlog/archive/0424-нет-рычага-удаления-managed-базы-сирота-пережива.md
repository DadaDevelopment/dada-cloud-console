---
id: 0424
status: closed
prio: P2
title: Нет рычага удаления managed-базы: сирота переживает и апп, и цикл
created: 2026-08-21
sess: sess-0821d
closed_at: 2026-08-21
closed_commit: 22decded
closed_note: Премиса пункта была наполовину неверна и исправлена по коду: рычаг удаления managed-базы ЕСТЬ и в API (router.go:425 -> databases.go:616 DeleteServiceDatabase, 202+operation), и в UI (frontend/lib/api.ts:457, databases/[name]/page.tsx). Чего нет — реклейма: у Crossplane Database/Role внутри композиции ServiceDatabaseV2 стоит deletionPolicy: Orphan, DROP не делает никто, поэтому база и роль переживают удаление и видны только в /admin/db-shards как orphan=true. Отгружено 22decded: текст под кнопкой больше не обещает стирание данных, появился гейт вводом имени (была асимметрия: restore требовал имя, delete — ничего), deleteDatabase оставлен вне MCP-allowlist с записанной причиной. Пруф двухполюсный на риге: RED на старой строке и на отсутствии deleteConfirmName, GREEN # pass 374 / fail 0, tsc 0, go test ./internal/mcp ok. Остаток вынесен в новый пункт.
---
Живьём 2026-08-21 [live Dada MCP listDatabases]: ServiceDatabaseV2 `tlsprobe` (`agent-sandbox/prod`, id `64845488-e2ad-4cc9-8522-7ffd6d352fae`, phase `Ready`, `live_at=2026-08-21T00:55:51Z`) создана агентом этого цикла и снята быть НЕ может: в Dada MCP нет `deleteDatabase` — только `createDatabase` / `listDatabases` / `getDatabaseCredentials`. Прямого psql к shard-0 у машины тоже нет (сеть до прода мертва третий цикл).

Тот же класс, что 🔴 Orphan из sess-0814L, где managed-база пережила удаление аппа: строка в консоли исчезает, база на шарде живёт и занимает место на платном хранилище.

Что нужно: путь удаления managed-базы (API + UI), с преflight (0 соединений) и пруфом отсутствия. Иначе каждый удалённый апп с базой оставляет платный мусор, а агент/юзер не имеет рычага прибраться.

Побочный долг: снять `tlsprobe` (DROP DATABASE + DROP OWNED + DROP ROLE svc-tlsprobe) первым делом в цикле, у которого будет сеть.
