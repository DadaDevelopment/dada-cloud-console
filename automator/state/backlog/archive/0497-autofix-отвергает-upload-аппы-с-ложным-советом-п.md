---
id: 0497
status: closed
prio: P1
stream: 3
hypothesis: H08
title: Autofix отвергает upload-аппы с ложным советом «переподключите через GitHub App»
created: 2026-09-07
sess: sess-0907a
closed_at: 2026-09-07
closed_commit: ed6bc0b0
closed_note: Честный вердикт для upload-аппов: 400 code=upload_app_no_git с советом подключить git вместо невозможного "переподключите через GitHub App"; кнопка скрыта для source=archive; i18n ru/en. Verified: internal/api green на чистом PG (74s), gofmt/vet/tsc чисто, ed6bc0b0 в origin/main.
---
Симптом [live psql 2026-09-07]: ivakinavv23@yandex.ru (рег 09-03, upload-флоу) собрал jkjk -> build failure -> ДВАЖДЫ TriggerAutofix -> 400 reason=launch_failed "репозиторий подключён без доступа GitHub App: переподключите его через GitHub App" (autofix.go:259, errRepoWithoutInstallation из resolveGitRepo cloud_tasks_store.go:172). Совет невыполним: у upload-аппа НЕТ git-репозитория, repo_full_name='upload/<app>' (uploadsource.go:234). Терминальное действие юзера = этот отказ, юзер потерян.
Механика: кнопка autofix видна на карточке упавшей сборки ЛЮБОГО аппа (E75), включая upload. Каждый upload-юзер с упавшей сборкой упирается в ту же стену.
Фикс этого цикла: честный вердикт для upload-аппов (дискриминатор strings.HasPrefix(repo,"upload/")): отдельный code=upload_app_no_git, сообщение "автофикс чинит через git-PR; подключите git-репозиторий — и автофикс заработает", фронт: не рисовать кнопку/рисовать подсказку для upload-аппа.
Стратегический хвост (отдельный пункт): реальный канал upload-autofix — агент правит загруженный архив (presigned download уже есть, source_archive_download.go) и перезаливает -> TriggerBuild. 
