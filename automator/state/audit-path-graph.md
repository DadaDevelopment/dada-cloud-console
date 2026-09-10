# Audit path graph - 2026-09-10 (sess-0910a)

## Окно: 24ч (с прошлого цикла 09-09 06:31)

Новых юзеров: 1 - y4ndex.danila@yandex.ru (SignUp 09-09 12:03 UTC).

### Путь danila (полный, [live psql dada-cloud MCP])
1. 12:03:13 SignUp + SessionStart
2. 12:03:13 CreateProject (y4ndex-danila-yandex-ru) - автопроект за 0.3с после рега
3. 12:03:13 ViewProject, ViewApps
4. 12:04:29 UploadSourceArchive (app "smirad") - ПОТОК 1 (upload без git), первое действие через 76с
5. 12:04:29 ViewBuildLogs - смотрит логи упавшего билда

Билд smirad упал: framework_undetected - в архиве нет package.json/requirements.txt/pyproject.toml/go.mod/pom.xml/build.gradle/Dockerfile [live: панель failed_builds, error_message].

Хвост пути после 12:04:40 не дочитан (MCP dada-cloud queryDatabase падает на nullable text-колонках audit_events: "bind message has 2 result formats but query has 4 columns"; cooldown ~53с; 4 попытки). Осторожно: MCP-инструмент хрупкий на срезах с resource_name/-id.

### UX-вывод (обязателен)
 Leak: новичок грузит архив "просто папку с кодом" без распознаваемого манифеста и получает отказ. Ошибка УЖЕ объясняет что нужно (add one of those manifests) - но юзер-вайбкодер с Lovable/Bolt-экспортом может не иметь их вообще.
 Действие: bl add 0498 - auto-detect должен предлагать шаблон-обёртку (сгенерить минимальный Dockerfile/static-server) при framework_undetected для zip/folder-путей (поток 1, H11). Не строить в этом цикле - сначала посмотреть полный путь danila (дочитать после cooldown).

## Полюса E156 [live]
 actor_type: user=6415, system=3485 - оба ненулевые (полюс 1 OK). Срез system за 30м = 0 строк - фоновые акторы не спамят. Полюс 2 (исключение system из срезов) подтверждён работой join по actor_type='user'.

## Панель: not_ready_other = unmaintained инфра-хвосты (payments/ai-gateway/zerkalo - PublicApi без ingress, phase Unknown) - не юзерские, не трогать (решение повторено 09-10, владелец в курсе).
