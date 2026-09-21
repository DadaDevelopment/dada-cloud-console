---
id: 0514
status: open
prio: P2
stream: 1
hypothesis: H11
title: gitingest: апстрим переехал cyclotruc -> coderamp-labs, карточка ссылается на редирект
created: 2026-09-21
sess: sess-0921a
---
[live registry+github 09-21] Карточка gitingest (catalog.go) держит Repo "cyclotruc/gitingest",
но репозиторий переехал в организацию coderamp-labs; cyclotruc/gitingest теперь редирект.
Официальный образ публикуется как ghcr.io/coderamp-labs/gitingest.

Иммутабельного semver-тега у апстрима нет: только катящийся main и коммит-теги вида
main-<sha>-<ts>. Порт из конфига образа = 8000/tcp (не 80!), плюс 9090 метрики.

Карточка сейчас на билд-треке (Framework dockerfile) и наследует ровно тот риск,
который убил it-tools. При переводе на image-track: пинить main-<sha>-<ts>, НЕ main,
и поправить Repo/IconRepo на coderamp-labs, иначе логотип и ссылка ведут на редирект.
