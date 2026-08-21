---
id: 0388
status: closed
prio: P0
stream: 1
hypothesis: H02
title: FinishGitAppInstall не пишется при успешном коннекте: kkartov подключил репо, строки нет
created: 2026-08-20
sess: sess-0820d
closed_at: 2026-08-20
closed_commit: 5b8e413d
closed_note: Обе гипотезы пункта ОПРОВЕРГНУТЫ, вопрос закрыт другим ответом. (B) снята в лоб: все 4 строки FinishGitAppInstall за 14д несут непустой install_nonce и все 4 джойнятся со стартом — 0/4 потерь на джойне. (A) снята обходом всех терминальных веток: GitInstallCallback (gitrepos.go:335-390) и GitHubOAuthCallback (git_oauth.go:117-206) зовут recordInstallVerdict/recordOAuthVerdict на КАЖДОМ выходе, включая провальные (missing_installation_id, resolve_installation_failed, save_installation_failed); у каждого актора, у кого установка реально привязалась (bruzas, lifecoachrussia, michaelharlam), Finish ровно один и он матчится. Настоящий ответ: у kkartov 0 строк в git_app_installations ВООБЩЕ, и ноль Finish ЛЮБОГО исхода — значит его 13 перелётов до коллбэка не доехали ни разу, это подлинный отвал. А его 6 успешных ConnectGitRepo прошли ДРУГИМ путём: linkGitRepo (gitrepos.go:1409+) принимает пустой installation_id и собирает анонимный https://github.com/<repo>.git, который App вообще не требует. Прошлая сессия читала ConnectGitRepo success как доказательство успеха перелёта — это ошибка области измерения, а не пропуск записи.
---
Вскрыто при разборе 0387 (sess-0820d, [live psql]). Второй дефект того же прибора, независимый от
render-эмиссии `StartGitAppInstall`.

Улика: `kkartov@yandex.ru` (рега 2026-08-17) — 13 стартов перелёта, **ни одного** `FinishGitAppInstall`.
По нашей метрике он «не доехал ни разу». Но через **108 секунд** после последнего старта в аудите стоит
`ConnectGitRepo` на `GitRepo instatic` с `outcome=success` — то есть GitHub-подключение РЕАЛЬНО состоялось,
а событие финиша не написалось.

Почему это дороже, чем кажется: Finish — это числитель ЕДИНСТВЕННОЙ метрики двери. Если он пропускает
успехи, то смертность перелёта завышена с ОБЕИХ сторон одновременно: знаменатель раздут рендером (0387),
числитель занижен пропуском. Любая правка «двери», приоритизированная по этому числу, будет чинить не то.

Корреляция идёт по `metadata->>'install_nonce'` (`state/git-oauth-flight.sql`). Две конкурирующие
гипотезы, обе НЕ проверены — это и есть работа пункта:
1. событие не пишется на каком-то из путей успеха (`GitInstallCallback`/`recordInstallVerdict`,
   `backend/internal/api/gitrepos.go:383`; `GitHubOAuthCallback`/`recordOAuthVerdict`, `git_oauth.go:180`);
2. событие пишется, но с nonce, который не совпадает со стартовым — тогда джойн его теряет, и строка
   есть, но невидима запросу. Проверяется в лоб: `select` всех `FinishGitAppInstall` за окно БЕЗ джойна
   и сверка их nonce со стартовыми.

Отдельная ветка, которую надо исключить: `setup_action=update` и выбор «select repositories» вместо
«all repos» могут вести коллбэк по пути, где вердикт не пишется.

M2: для случая `kkartov` названо, ПОЧЕМУ строки нет (код или nonce), правка отгружена, и повторный прогон
`git-oauth-flight.sql` даёт числитель, совпадающий с числом реальных `ConnectGitRepo success`.
