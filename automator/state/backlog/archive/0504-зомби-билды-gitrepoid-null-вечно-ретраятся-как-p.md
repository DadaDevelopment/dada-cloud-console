---
id: 0504
status: closed
prio: P1
stream: 2
hypothesis: H02
title: Зомби-билды (git_repo_id NULL) вечно ретраятся как platform_error
created: 2026-09-15
sess: sess-0915a
closed_at: 2026-09-15
closed_commit: 9e2ea98c
closed_note: Зомби-билды убиты: guard repo_detached в runner.run до LoadRepo, RequeueForRetry/RetryPlatformFailedBuilds отказывают NULL-repo, миграция 155 закрывает in-flight хвост. Тесты на реальной scratch-БД (155 миграций), мутационная проверка гварда RequeueForRetry, полный сьют db-пакета ok 94s, worker ok, go vet/gofmt чисто, push 9e2ea98c.
---
Зомби-билды удалённых аппов: git_repo_id=NULL (FK SET NULL, mig 116 + deleteAppGitRepo dbwatcher.go:1525) гоняются self-heal по кругу как platform_error (load repo 0000), юзер теряет день и удаляет апп
