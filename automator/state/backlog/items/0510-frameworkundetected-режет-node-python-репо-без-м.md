---
id: 0510
status: open
prio: P1
stream: 1
hypothesis: H11
title: framework_undetected режет node/python-репо без манифеста: 3 из 16 новых юзеров умерли здесь
created: 2026-09-20
sess: sess-0920a
---
[live psql 09-20, окно 30д] 3 из 16 новых юзеров (saravananofficial13, ivakinavv23, y4ndex.danila)
имеют ТЕРМИНАЛЬНЫМ действием BuildFinished/failure с fail_reason='framework_undetected' или
'dockerfile_build_failed'. Это 3 из 5 юзеров окна, оставшихся вообще без живого приложения (5/16 = 31%).

Фикс 86d64424 (09-10) закрыл ЧАСТНЫЙ случай - голая папка с html = деплоимый сайт. Обычный
node/python-репозиторий без распознаваемого манифеста по-прежнему завершает билд ошибкой.

Что происходит с юзером: он доходит до UploadSourceArchive/ConnectGitRepo (то есть прошёл ВСЮ
воронку до последнего шага), получает failure, иногда жмёт TriggerAutofix (тоже failure) и уходит
навсегда. Это самый дорогой момент для потери - вся предыдущая работа юзера уже вложена.

Что сделать: при framework_undetected не завершать билд ошибкой, а возвращать юзеру выбор
рантайма / мастер Dockerfile прямо на странице сборки. Детектор: backend/internal/sourcedetect/.
Критерий успеха: доля новых юзеров с терминальным BuildFinished/failure падает; measure на
следующей когорте в 30д.
