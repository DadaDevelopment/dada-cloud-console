# Upload-deploy: папка/файл/архив → auto-detect → deploy (поток 1, STRATEGY execution-bet)

Status: SPEC (P0-1b). Grounded 2026-07-23 на живом коде тремя read-only проходами (backend, build-agent+jenkins-pipelines, frontend). Все file:line ниже — из этих проходов.

## Зачем

Единственный измеренно работающий активационный механизм — деплой без git-стены (template anon-clone, 3/6 незнакомцев активировались, E19). Upload генерализует его: вайбкодер экспортирует zip из Lovable/Bolt/v0 и получает live URL без GitHub вообще. Целевая ЦА уже наблюдаема (artem, ggrk52 класс).

Scope-ограничение (owner): inline браузер-редактор НЕ строить. Только drop архива/файла/папки.

## Ключевые факты из grounding (что уже есть / чего нет)

| Факт | Где |
|---|---|
| Клон исходников делает НЕ build-agent, а Jenkins: `stage('clone')` → `git clone --depth 1 → src/` | jenkins-pipelines/vars/dadaBuildPipeline.groovy:100-103 |
| Весь остальной пайплайн зависит только от каталога `src/` (CTX, Dockerfile-детект, buildx, маркер `==> image:`) | groovy:108-145 |
| build-agent отдаёт Jenkins параметры `repo/branch/framework/...` из строки `git_repos` (поллер по `idx_builds_queued`) | build-agent/internal/worker/runner.go:532-572, db/repos.go:47-57 |
| Anon-путь уже существует: `installation_id==0` → пустой токен + голый clone URL | runner.go:926-930 |
| `git_repos.provider` CHECK IN ('github','gitlab') — расширяемая точка для нового source | backend/migrations/013_git_build_deploy.sql:34 |
| S3-клиент (minio-go) уже есть в backend, но только presigned GET | backend/internal/cloudtask/dbbackup_presign.go:35-57 |
| Multipart-upload в backend НЕТ нигде; ingress режет body: console 10m, gateway 16m | helm/dada-cloud-console/templates/ingress.yaml:9, gateway-ingress.yaml:16 |
| DetectForBuild читает файлы через GitHub Contents API (не локальный checkout) → для архива НЕ работает | build-agent/internal/server/server.go:885, 558 |
| В build-agent нет ни S3, ни archive/zip/tar кода — greenfield | grep-верифицировано |
| Frontend: apiFetch JSON-only (hardcoded Content-Type), file-upload UI нет нигде | frontend/lib/api.ts:123-151 |
| DeployChooser: `DeployKind = git\|image\|compose`, grid из 3 карт — слот для 4-й | frontend/components/deploy/deploy-chooser.tsx:10,57-61 |
| TriggerBuild: резолвит git_repos row, вставляет build status=queued, commit_sha=`manual-<ts>` | backend/internal/api/builds.go:209-271 |
| Последняя миграция 040_feedback.sql → следующая 041 | backend/migrations/ |
| Vite SPA / wired-port / SPA-fallback уже починены в пайплайне (2cd8aaf) — Lovable/Bolt экспорт (Vite) билдится | jenkins-pipelines + memory E-door-gate |

## Дизайн (минимальный, реюз максимума)

Принцип: архив = ещё один «provider» строки `git_repos`, дальше ВЕСЬ существующий конвейер (builds table → поллер → Jenkins → marker → HandoffDeploy) не меняется. Меняются три узкие точки: как исходники попадают в S3, как Jenkins получает их вместо clone, и откуда берётся detect.

### 1. Хранение артефакта
- Реюз существующего S3 (Beget) и конфиг-паттерна `DB_BACKUP_S3_*`: новые env `SOURCE_UPLOAD_S3_*` (endpoint/bucket/region/keys могут совпадать со значениями backup), prefix `source-uploads/`.
- Ключ: `source-uploads/{org}/{project}/{app}/{uploadID}.tar.gz` (zip конвертируем? нет — храним как загрузили, формат в метаданных ключа/расширении; поддержка `.zip` и `.tar.gz`).
- Retention: не чистим в MVP (объёмы копеечные, cap 100MB/архив).

### 2. Backend (dada-cloud, slice A)
- **Миграция 041**: `ALTER TABLE git_repos DROP CONSTRAINT ... provider CHECK` → пересоздать с `IN ('github','gitlab','archive')`. Никаких новых колонок: S3-ключ кладём в `clone_url` (`s3://<bucket>/<key>`), `installation_id` NULL, `production_branch`='upload', `repo_full_name`=`upload/<app>`.
- **Endpoint 1 — upload**: `POST /api/v1/projects/:projectId/environments/:envId/apps/:appName/source-archive` (multipart, поле `archive`). Стримит `http.MaxBytesReader(100MB)` → minio `PutObject`. Возвращает `{artifact_uri, detected:{framework,port}}`.
  - Auth: `canWrite(role)` + scope `builds:write` (как TriggerBuild, router.go:381).
  - **Детект НА БЭКЕ в момент upload** (у нас единственная точка где есть байты): прочитать оглавление архива + вытащить манифесты (`Dockerfile`→EXPOSE, `package.json` (vite/next/react-scripts), `requirements.txt`/`pyproject.toml` (fastapi/flask/django/streamlit), max N файлов, max 1MB на файл, zip-slip guard на путях). Логика — упрощённый порт resolveExplicitPort (server.go:612), живёт в backend `internal/sourcedetect`. Результат → `framework_override`, `port` в git_repos row.
  - Идемпотентность: повторный upload на тот же app перезаписывает clone_url (новый uploadID) — это и есть «редеплой» для upload-приложений.
- **Endpoint 2 — create+build**: реюз. Если git_repos row нет — endpoint 1 сам делает UPSERT строки (provider='archive') и вставляет build `status='queued'` (тот же INSERT что builds.go:266-271, trigger='manual'). Отдельного endpoint НЕ нужно — один запрос = upload+detect+queue. Меньше round-trips, меньше OpenAPI-поверхности.
- **Swag regen обязателен** (`swag init -o internal/api/docs`) — TestOpenAPICoverage гейтит CI.
- **Ingress**: `proxy-body-size: "110m"` на console ingress (helm/dada-cloud-console/templates/ingress.yaml:9) и gateway-ingress.yaml:16. Timeouts уже 3600s. Presigned-PUT-в-обход рассмотрен и отвергнут: Beget S3 CORS из браузера непроверен, двухфазный протокол, а поднять аннотацию — одна строка. Rejected-alternative зафиксирована.

### 3. build-agent (slice B1)
- `gitCreds` (runner.go:926): новая ветка `repo.Provider == "archive"` → минио-клиент (скопировать паттерн dbbackup_presign.go, конфиг `SOURCE_UPLOAD_S3_*`) presigned GET (TTL 1ч) на ключ из `CloneURL` → вернуть как новый param `archive_url` (не `repo`).
- `execute()` (runner.go:532): если archive-режим — params `archive_url` + `branch` заглушкой `upload-<uploadID-8>` (из него Jenkins делает TAG/CACHE_REF, groovy:108-111).
- Детект не вызывать (он GitHub-only): framework/port уже лежат в git_repos row с момента upload (см. 2).

### 4. jenkins-pipelines (slice B2, репо jenkins-pipelines, ветка develop)
- Новый необязательный параметр `archive_url` (декларации groovy:55-64).
- `stage('clone')` (groovy:100-103): ветка — если `archive_url` непуст: `curl -fsSL "$archive_url" -o src.bin` → по magic-байтам `unzip`/`tar xzf` в `src/` c защитой от zip-slip (`--strip-components` для одно-корневых архивов: если в архиве ровно один top-level dir — срезать его; Lovable/Bolt zip именно такие). Иначе прежний git clone.
- Ниже по пайплайну изменений НЕТ (Dockerfile-детект, buildx, marker работают от `src/`).
- Деплой-порядок: groovy + build-agent идут ПЕРВЫМИ (новый параметр безвреден пока никто не шлёт), backend/frontend после. Тот же урок что autofix hub-first (backlog P1-3b).

### 5. Frontend (slice C)
- `DeployChooser`: 4-я карта `kind="upload"` (deploy-chooser.tsx:10,57-61), i18n `apps.deploy.fromUpload.*` в lib/i18n/console/messages/apps.ts.
- Overview hero + apps empty-state: upload-карта РЯДОМ с TemplateDeployCards (сосед, не внутрь грида — page.tsx:139, apps/page.tsx:457) — это второй «безgit-путь» на экране 43%-leak.
- Новый компонент `components/deploy/upload-deploy.tsx`: drag-drop zone (input type=file + onDrop, accept .zip/.tar.gz; папка → webkitdirectory в v2, НЕ MVP), поле app name (APP_NAME_RE, apps/page.tsx:151), порт (prefill из detected), прогресс через XHR/fetch upload progress.
- api.ts: новый helper `apiUpload(path, formData, onProgress)` — без JSON Content-Type, таймаут 10 мин (30s в apiFetch:135 не годится), тот же bearer из getToken().
- После 2xx: редирект на builds конкретного app (BuildLogViewer уже есть).

### 6. Что НЕ делаем в MVP (отложено сознательно)
- Папка (webkitdirectory) и одиночный файл (main.py) — v2; MVP только архив. Одиночный файл = обернуть в архив на клиенте, отдельный кусок.
- Auto-detect за пределами манифестов (никакого nixpacks) — Dockerfile-путь пайплайна уже покрывает всё остальное, а бездокерфайльные web-фреймворки покрыты detection-параметрами из backend-детекта.
- Чистка старых артефактов, версии/rollback архивов, CDN.
- CLI (`dada deploy .`) — отдельная ставка, после замера web-flow.

## Риски / gotchas
1. **Порядок деплоя 3 репо**: jenkins-pipelines → build-agent → backend → frontend. Нарушение = builds с archive_url который Jenkins не знает → молчаливый git clone мусорного URL. Проверка в groovy: неизвестный source → fail fast с понятным логом.
2. **Zip-slip**: пути с `..` в архиве. Гвард и на backend-детекте (пропуск таких entries) и на extract в Jenkins (`tar --no-absolute-names`, unzip в чистый dir + проверка realpath).
3. **Ingress 10m сейчас**: пока аннотация не поднята, upload >10m умрёт на nginx c 413 ДО backend — поднять в том же PR что endpoint.
4. **provider CHECK**: миграция должна пересоздавать constraint идемпотентно (паттерн 013/023/037, `IF EXISTS`).
5. **worker/no-domain контракт**: upload-app проходит обычный CreateApp/HandoffDeploy путь → surrogate-домен выдаётся как обычно (это и нужно: live URL — суть фичи).
6. **fetch/XHR upload progress**: fetch не даёт прогресс без ReadableStream-стрима; XHR проще — взять XHR в apiUpload.

## Слайсы и M2

| Slice | Репо | Суть | Гейт |
|---|---|---|---|
| B2 | jenkins-pipelines | archive_url param + extract-ветка clone-stage | groovy replay на тестовом job; безвредность без параметра |
| B1 | dada-cloud/build-agent | provider=archive → presign GET → params | go build/vet/test; anon/github пути не тронуты (unit) |
| A | dada-cloud/backend | mig 041 + upload endpoint + sourcedetect + swag + ingress bump | go test, TestOpenAPICoverage, миграция идемпотентна |
| C | dada-cloud/frontend | chooser card + upload-deploy.tsx + apiUpload + i18n | tsc, eslint, next build |

**Финальный M2 (P0-1c)**: реальный zip незнакомого проекта (Vite-экспорт + FastAPI со своим Dockerfile — 2 архетипа) → drop в консоль → build SUCCESS → live URL curl 200. Не build-green, а живой URL.

Оценка: A+B в один цикл впритык; реалистично B2+B1 цикл 1, A цикл 2, C цикл 3, M2 цикл 3-4.
