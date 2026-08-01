# App File Manager (FS browser + editor)

## Что уже есть

- `backend/internal/cloudtask/podexec.go` — SPDY exec в под приложения (RBAC `pods/exec` уже выдан, работает в проде).
- `backend/internal/api/volume_export.go` — `tar czf -` всего тома → S3 → presigned URL (15 мин). Кнопка в Storage settings.
- `backend/internal/api/volume_usage.go` — заполненность тома.
- PVC приложений — **ReadWriteMany** (`gitops-agent/internal/renderer/renderer.go:227`).
- Фронт: CodeMirror 6 уже в зависимостях (`components/ui/yaml-editor.tsx`), Radix UI kit, i18n ru/en.

Не хватает: просмотр дерева, чтение/правка/загрузка/удаление отдельных файлов.

## Решение

Своё, не Sprut.io (PHP+Python 2014, отдельный демон, свой auth — тянуть в k8s-платформу нечего).
Поверх существующего pod-exec: ~600 строк бэка + ~700 фронта.

### Phase 1 — exec в живой под приложения

`cloudtask.PodFS` интерфейс: `List, Stat, ReadFile, WriteFile, Mkdir, Move, Remove, TarDir, UntarTo`.
Реализация `podexecFS` = argv-команды через тот же SPDY-exec.

Инъекции нет by construction: путь передаётся позиционным аргументом, не интерполируется:

```
sh -c 'cd "$1" && ls -A | while read f; do stat -c "%n|%F|%s|%Y|%a" "$f"; done' _ "$PATH"
```

`stat -c` есть и в busybox, и в coreutils.

Защита пути (сервер):
1. `filepath.Clean`, обязан быть absolute и с префиксом `vol.Path`.
2. Запрет `..` после clean, запрет `\n`/`\x00`.
3. Symlink-escape: `readlink -f` результат должен остаться под `vol.Path`.

### Endpoints (scope приложения, как volume/export)

| метод | путь | право |
|---|---|---|
| GET | `/volume/files?path=` | reader |
| GET | `/volume/files/content?path=` (текст ≤1 MB, бинарь → 415) | reader |
| GET | `/volume/files/raw?path=` (стрим одного файла) | reader |
| GET | `/volume/files/archive?path=` (tar.gz каталога, стрим) | reader |
| PUT | `/volume/files/content` | writer |
| POST | `/volume/files/upload` (multipart, ≤100 MB, стрим в stdin) | writer |
| POST | `/volume/files/mkdir` \| `/move` \| `/delete` | writer |

Лимиты: 2000 записей на каталог, 1 MB на текстовое чтение/запись, 100 MB на загрузку, таймаут 5 мин.
Аудит: все мутации + `raw`/`archive` (это выгрузка данных).

### Phase 2 — helper pod (позже, шов уже заложен)

Если в образе нет `sh` (distroless/scratch) или под не Running — поднимать короткоживущий под с нашим статическим бинарём, монтирующий тот же PVC (RWX это позволяет). Тот же интерфейс `PodFS`, другая реализация. Даёт доступ к файлам упавшего приложения — то, что нужнее всего.

## Фронт

Раздел `/projects/:projectId/apps/:appName/files`, плоский Vercel-стиль:

- breadcrumb-путь, кнопки «Новая папка», «Загрузить», «Скачать .tar.gz».
- Список: иконка по типу, имя, размер, mtime, права; hover-меню (переименовать/удалить/скачать).
- Правая панель: CodeMirror 6, язык по расширению, `Cmd+S` — сохранить, дифф-варнинг при внешнем изменении (сравнение mtime перед записью).
- Бинарные файлы: превью картинок, иначе только «скачать».
- Drag-and-drop на весь список, прогресс загрузки.
- i18n ru/en, dark/light.

## Риски

- Образы без shell → 409 с понятным текстом до Phase 2.
- Большие каталоги → пагинация по 2000, серверный лимит.
- Конкурентная запись → проверка mtime перед PUT, 409 при расхождении.

## Статус (2026-08-01) — Phase 1 отгружена

Бэкенд:

- `backend/internal/cloudtask/podfs.go` — интерфейс `PodFS` + реализация через exec в живой под (POSIX-команды, пути только позиционными аргументами, никакой интерполяции в shell).
- `backend/internal/api/volume_files.go` — 9 хендлеров, двухслойная защита пути (лексический clamp + физический `cd && pwd -P` + разыменование симлинка), атомарная запись (temp + `mv`), аудит на все мутации и на `raw`/`archive`.
- Роуты в `router.go`, swagger перегенерён (`swag init -g cmd/server/main.go -o internal/api/docs`).
- Тесты: `podfs_test.go` (парсер `stat`, argv-инъекция, fail-closed) и `volume_files_test.go` (clamping, symlink-escape, NUL, классификация ошибок + DB-бэкед хендлеры) — зелёные на реальной Postgres.

Фронт:

- `frontend/lib/api.ts` — `filesApi` (list/read/write/mkdir/move/delete/upload/download/objectUrl); скачивание идёт через blob с bearer-токеном, потому что обычная навигация не несёт `Authorization`.
- `frontend/components/files/file-browser.tsx` — двухпанельный менеджер: путь-крошки, фильтр, drag-and-drop с прогрессом, контекстное меню, модалки «новая папка»/«переименовать»/«удалить», тосты.
- `frontend/components/files/file-editor.tsx` — CodeMirror 6, YAML-грамматика для `.yaml/.yml`, тема по теме консоли, `Cmd/Ctrl+S`.
- Страница `/projects/:projectId/apps/:appName/files`, вход из карточки приложения и из настроек хранилища.
- i18n `apps.files.*` ru/en.

Не сделано (осознанно): Phase 2 (helper pod для distroless и упавших приложений), подсветка языков кроме YAML (в консоли нет других codemirror-грамматик — новые npm-зависимости не тянул).
