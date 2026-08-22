# MCP feedback — план работ

Источник: фидбек владельца 2026-08-21 (стирание values.yaml через setEnvVar на telemost-bot,
+ эргономика адресации).

## Корень (подтверждён по коду)

- `gitops-agent/internal/renderer/values_merge.go:20` — `ownedCommonKeys` включает
  `extraEnv`, `servicePort`, `useDotEnv`. Молчание рендера по owned-ключу = удаление
  (`deleteMapKey`, values_merge.go:92). Для аппа, собранного руками в argo-infra, БД
  не знает этих ключей, поэтому env-apply их удаляет.
- Страж существует (`DroppedPaths`), но вызывается только при `op.Unattended()`
  (`dbwatcher.go:2426`), а MCP ходит под сервис-аккаунтом → не unattended → пропуск.
- Страж к тому же считает дроп по СЫРОМУ рендеру, а не по результату merge — то есть
  переоценивает потери (`common.ingress` merge сохраняет, а страж считает утраченным).
  Из-за этого его нельзя было включить на все деплои.
- `listEnvVars` читает только таблицу `env_vars` (`backend/internal/api/envvars.go:215`) —
  пустой ответ у аппа с 12 живыми переменными честен, но читается агентом как «можно писать».

## Задачи

- [x] A. Страж клоббера на ВСЕХ деплоях аппа, считать дроп по merged-результату,
      вычитать намеренные удаления из payload (`expected_drops`).
- [x] B. `listEnvVars` двухисточниковый: строки БД + живой Deployment
      (`valueFrom.secretKeyRef`, `envFrom`), поле `source`.
- [x] C. Dry-run: `setEnvVar`/`deleteEnvVar` с `dry_run` → операция рендерит,
      пишет дифф и дропы в `validation_result`, ничего не коммитит.
- [x] D. Адресация по именам: `GET /api/v1/resolve` (resolveRef) + приём
      `project`/`env`/`app` вместо UUID в MCP-прокси.
- [x] E. `listApps`: обязательная фильтрация, тощий ответ (`view=summary`).
- [x] F. Имена рядом с id во всех ответах, где есть `app_id`/`environment_id`.

## Итог (2026-08-22)

- A — `guardValuesClobber` на всех деплоях аппа, дроп считается по merged-файлу,
  `expected_drops` в payload разрешает намеренные удаления.
- B — `listEnvVars` отдаёт `env_vars` + `cluster_env` (ключи из `secretKeyRef`,
  `configMapKeyRef`, `.env`), с `observed`, чтобы «не смогли посмотреть» не читалось
  как «пусто».
- C — `dry_run` у `setEnvVar` (body) и `deleteEnvVar` (query): строка не пишется,
  операция кладёт план (`added/changed/removed/would_block/verdict`) в
  `validation_result`, коммита нет. Ключ, который собирались записать, подмешивается
  в рендер (`overlayPendingEnv`) — в payload едут только ИМЕНА ключей.
- D — `GET /api/v1/resolve` (`resolveRef`) + MCP принимает `ref`/`project`/`env`/`app`
  вместо UUID у любого инструмента; env можно опустить, если окружение одно,
  иначе отказ со списком кандидатов.
- E — `listApps` фильтруется по `name`, `view=summary` (MCP ставит по умолчанию).
- F — в ответах рядом с id едут `project`/`env`/`name` и готовый `ref`.

Тесты: `backend/internal/api` (resolve/summary/dry-run), `backend/internal/mcp`
(addressing), `gitops-agent/internal/worker` (plan/overlay) — все RED-проверены.
