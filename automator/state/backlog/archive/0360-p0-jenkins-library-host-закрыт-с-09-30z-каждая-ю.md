---
id: 0360
status: closed
prio: P0
stream: 6
title: P0-JENKINS-LIBRARY-HOST ЗАКРЫТ — с 09:30Z каждая юзерская сборка умирала на загрузке dada-tuda-jenkins-pipelines@develop с уничтож
created: 2026-08-13
sess: sess-0813n
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
closed_at: 2026-08-13
closed_commit: 44f4aa76
---
- [x] ✅ P0-JENKINS-LIBRARY-HOST ЗАКРЫТ (sess-0813n, 2026-08-13 12:27Z, [live]) — с 09:30Z каждая юзерская сборка умирала на загрузке `dada-tuda-jenkins-pipelines@develop` с уничтоженного `bitbucket.dada-tuda.ru:7999`. Библиотека переехана на `https://github.com/DadaDevelopment/jenkins-pipelines.git`. Пруф не конфигом, а продуктом: `agent-sandbox` upload архива → `dada-build` #334 грузит библиотеку с github → `success` за 73 с → под 1/1 → `https://libfix-probe-bdde35.dada-tuda.ru/` = 200 `libfix-probe ok` (97 ms). Полный путь upload→живой HTTPS ≈ **7 минут**. Пробник снесён в том же цикле. Классификация такой аварии как `platform_error` уже в проде (`44f4aa76` в `92f8f6ab`).
