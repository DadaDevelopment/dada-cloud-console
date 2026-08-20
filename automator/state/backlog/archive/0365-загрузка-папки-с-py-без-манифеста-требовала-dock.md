---
id: 0365
status: closed
prio: P3
stream: 6
title: ЗАГРУЗКА ПАПКИ С .py БЕЗ МАНИФЕСТА ТРЕБОВАЛА DOCKERFILE
created: 2026-08-13
sess: sess-0813n
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
closed_at: 2026-08-13
closed_commit: 770a5197
---
- [ ] ✅ ЗАГРУЗКА ПАПКИ С .py БЕЗ МАНИФЕСТА ТРЕБОВАЛА DOCKERFILE — ОТГРУЖЕНО (sess-0813n, 2026-08-13, [live /admin/overview + code], `770a5197`) — живой юзер `genagent` три раза подряд получил `no_dockerfile: framework '' has no template and repo ships no Dockerfile`. Архив — скриптовый python (`agent.py`, `serve.py`, `service.py`, `importer.py`, `openlist.py`, `connectors/*.py`), ни requirements.txt, ни pyproject.toml, ни Dockerfile. `sourcedetect.Detect` требовал манифест и отдавал `""`, хотя python-ветка `dadaBuildPipeline` ровно эту форму собирает (`no python manifest - skipping install`, старт перебирает `main.py`/`bot.py`/`app.py`, затем любой `*.py`). Фикс: фолбэк «`.py` в корне архива = python» + `listEntries` теперь отдаёт полный список членов (entries хранит только манифесты-кандидаты, а у такого архива кандидатов НЕТ вовсе — пусты и entries, и выведенный из них root). 2 регресс-теста, в т.ч. «`.py` внутри node-репо НЕ перекрашивает сборку». Доставку и живой прогон на песочнице — дожать.
