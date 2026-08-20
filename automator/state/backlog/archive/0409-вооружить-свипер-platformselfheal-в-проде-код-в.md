---
id: 0409
status: closed
prio: P0
stream: reliability
hypothesis: H02
title: Вооружить свипер platform_selfheal в проде: код в main, флаг выключен, пострадавший всё ещё лежит
created: 2026-08-20
closed_at: 2026-08-20
closed_commit: 5b8e413d
closed_note: Свипер вооружён в проде и ВЫЛЕЧИЛ пострадавшего за 4 минуты. Проверено по порядку из пункта: прод-образ 59a9faf2 несёт e436ed91 (is-ancestor против ТЕГА ПОДА), миграция 135 применена 07:05:11Z, кандидатский SELECT прогнан руками и вернул ровно одну строку (gulyaev-ai-core, github, main). Флаг поднят через argo-infra 5b8e413d; configmap доехал за ~40с, но деплой монтирует его через envFrom и НЕ несёт checksum/config — пришлось дёрнуть rollout restart, иначе флаг лежал бы в configmap при старом env в поде. Вердикт мерен не логом: audit PlatformSelfHealRebuild pending+success (op 0479aa5f), claim selfheal_rebuilt_at=07:15:06, билд 20682777 success 07:16:40 с новым дайджестом 0136b4ab, RS 76dd5b59b5 1/1 Running 0 рестартов, uvicorn поднялся, https://gulyaev-ai-core-e0c79b.dada-tuda.ru/ = 200. Апп лежал 5 суток.
---
Механизм отгружен (`e436ed91`, `backend/internal/api/platform_selfheal.go` + миграция 135),
но `PLATFORM_SELFHEAL_ENABLED=false` и образ прода ещё не пересобран. Пока не вооружён —
`gulyaev-ai-core` (`lifecoachrussia@yandex.ru`, единственная регистрация за 48ч) продолжает
лежать в CrashLoopBackOff: отгруженный рычаг структурно недостижим
(память `project_shipped_lever_can_be_structurally_unreachable`).

Шаги:
1. Убедиться, что образ прод-консоли содержит `e436ed91` — `is-ancestor` против тега
   РАБОТАЮЩЕГО пода, не против `main`.
2. Проверить, что миграция 135 применена в проде.
3. Поднять `PLATFORM_SELFHEAL_ENABLED=true` в configmap `dada-cloud-console-config` через
   `dada-argo`. Env приходит через `envFrom` — чтение `.spec...containers[].env` даёт
   ложный негатив.
4. Вердикт мерить НЕ логом воркера, а строкой аудита `PlatformSelfHealRebuild`
   `outcome=success` + новой `success`-сборкой `gulyaev-ai-core` + подом `1/1 Ready`.

Опасность: свипер ставит билды в ЧУЖИЕ репозитории. Перед вооружением прогнать кандидатский
SELECT в проде вручную и убедиться, что список ровно тот, что ожидается (сегодня это 1 апп).
