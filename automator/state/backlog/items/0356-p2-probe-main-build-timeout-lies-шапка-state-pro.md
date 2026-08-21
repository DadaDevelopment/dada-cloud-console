---
id: 0356
status: open
prio: P2
stream: 6
title: P2-PROBE-MAIN-BUILD-TIMEOUT-LIES · Шапка state/probe-main-build.sh обещает ~40s, реально скрипт идёт 150-200s и на дефолтном тайма
created: 2026-08-11
sess: sess-0811e
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🟡 P2-PROBE-MAIN-BUILD-TIMEOUT-LIES (sess-0811e, 2026-08-11) · Шапка `state/probe-main-build.sh` обещает ~40s, реально скрипт идёт 150-200s и на дефолтном таймауте отдаёт `EXIT:124`. Пульс-гейт обязателен каждый цикл, значит цена вранья в шапке — ложное «пульс красный» в начале каждой сессии, которая поверит шапке. Поправить число в шапке и/или явно передавать `timeout 240`.
