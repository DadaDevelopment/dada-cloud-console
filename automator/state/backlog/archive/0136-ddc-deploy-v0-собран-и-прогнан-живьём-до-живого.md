---
id: 0136
status: closed
prio: P0
hypothesis: H02
title: ddc deploy v0 СОБРАН И ПРОГНАН ЖИВЬЁМ ДО ЖИВОГО URL (hypothesis: H02+H08+H11)
created: 2026-08-13
sess: sess-0813f
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
closed_at: 2026-08-13
closed_commit: 7bed90b0
---
- [x] ЗАКРЫТО ЧАСТИЧНО sess-0813f 2026-08-13 · `ddc deploy` v0 СОБРАН И ПРОГНАН ЖИВЬЁМ ДО ЖИВОГО URL (hypothesis: H02+H08+H11) — код в `cli/` (Go, без сторонних зависимостей): `ddc login` (device grant RFC 8628, клиент `ddc-cli`, токен в `~/.config/ddc/token.json` 0600) и `ddc deploy` (папка → tar.gz с уважением `.gitignore` и жёстким исключением `.git/node_modules/.next/dist/build/venv/__pycache__` → существующий upload-API → стрим статуса сборки → живой URL). Живой прогон в `agent-sandbox/prod` [live]: `Packaging 2 files` (node_modules исключён) → `Detected: node (port 3000)` → build `7bed90b0` `success` → `Live: https://ddc-cli-probe-136b0f.dada-tuda.ru` → внешний GET 200 с маркером. Уборка: DeleteApp `Committed` git `60586c78`, URL 404, подов/ingress/svc нет, локальный токен и папка снесены. ДВА НАСТОЯЩИХ БАГА ПОЙМАНЫ ДО ОТЧЁТА, НЕ АГЕНТОМ: (1) CLI просил скоупы `read builds:read deploy:write`, а роут upload гейтится `RequireScope("builds:write")` (`backend/internal/api/router.go:593`) → 403 после сборки архива; (2) терминальными считались `pushing`/`detecting`, а успех искался как `succeeded` — реальный словарь `queued|detecting|building|pushing|success|failed|canceled` (`build-agent/internal/db/builds.go:14`), из-за чего ПЕРВЫЙ живой прогон объявил успешную сборку `04cf1516` провалом. Оба закрыты тестами-контрактами. ЧТО НЕ ЗАКРЫТО (см. пункт ниже): (а) сам браузерный логин не прогонялся — агенту запрещено вводить креды в IdP, прогон шёл на подставленном в кэш сервисном токене, то есть device-нога доказана только до `user_code` [live], но не до выданного токена; (б) `source=cli` в аудите на проде НЕ виден — мидлварь `client_claimed` пока только в рабочем дереве, на проде метаданные пустые.
