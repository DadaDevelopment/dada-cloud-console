---
id: 0362
status: open
prio: P2
stream: 6
title: СВОДКА domains.failed=23 НА 96% — НАШ СОБСТВЕННЫЙ ТЕСТОВЫЙ МУСОР, НО ПАНЕЛЬ ЕГО НЕ ПОКАЗЫВАЕТ — разложил domains.failed=23
created: 2026-08-13
sess: sess-0813n
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🟡 СВОДКА `domains.failed=23` НА 96% — НАШ СОБСТВЕННЫЙ ТЕСТОВЫЙ МУСОР, НО ПАНЕЛЬ ЕГО НЕ ПОКАЗЫВАЕТ (sess-0813n, 2026-08-13, [live psql+kubectl]; **самокоррекция того же цикла**) — разложил `domains.failed=23`: реальная поломка у юзера ровно **1** (`reels-tracker-fe2427`), 21 сирота от удалённых аппов (из них **9 — агентские пробники в `agent-sandbox`**: `routine-upload-probe`, `ddc-cli-probe`, `routine-upload-flask`, `upload-node-test`, `upload-static-test`, `m2-live-probe`, `wedge-probe`, `gl-anon-probe`, `excalidraw-probe`), плюс 1 `attach_timeout`. **Первая формулировка была неверна**: список `domain_issues` в `/api/v1/admin/overview` в этот момент содержал ровно **2** строки — строки `status_reason='app_deleted'` исключаются из детализации ПО ЗАМЫСЛУ (`domain_hostname_reap_test.go:461`). Раздут не список, а сводный счётчик `domains.failed` — тот же дефект, что уже записан выше пунктом «СВОДНЫЙ СЧЁТЧИК ДОМЕНОВ РАЗДУВАЕТ ШУМ» (sess-0811i), теперь с новым замером. Две двери прежние: (а) `DeleteApp` обязан закрывать строку домена, а не оставлять сироту; (б) сводный счётчик привести к тому же предикату, что и детализация. Наш вклад в мусор — отдельная гигиеническая задача, а не поломка панели.
