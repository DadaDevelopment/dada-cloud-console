---
id: 0514
status: closed
prio: P2
stream: 1
hypothesis: H11
title: gitingest: апстрим переехал cyclotruc -> coderamp-labs, карточка ссылается на редирект
created: 2026-09-21
sess: sess-0921a
closed_at: 2026-09-22
closed_commit: 36629764
closed_note: Исполнено в 36629764: карточка на ghcr.io/coderamp-labs/gitingest:main-4e259a0-1755307869 (тег под HEAD апстрима), IconRepo coderamp-labs, порт 8000 подтверждён probe-ом из конфига образа.
---
[live registry+github 09-21] Карточка gitingest (catalog.go) держит Repo "cyclotruc/gitingest",
но репозиторий переехал в организацию coderamp-labs; cyclotruc/gitingest теперь редирект.
Официальный образ публикуется как ghcr.io/coderamp-labs/gitingest.

Иммутабельного semver-тега у апстрима нет: только катящийся main и коммит-теги вида
main-<sha>-<ts>. Порт из конфига образа = 8000/tcp (не 80!), плюс 9090 метрики.

Карточка сейчас на билд-треке (Framework dockerfile) и наследует ровно тот риск,
который убил it-tools. При переводе на image-track: пинить main-<sha>-<ts>, НЕ main,
и поправить Repo/IconRepo на coderamp-labs, иначе логотип и ссылка ведут на редирект.
