---
id: 0406
status: closed
prio: P0
stream: 2
hypothesis: H02
title: Половина фикса ModuleNotFoundError лежит незапушенной в dada-argo, вторая уже в проде
created: 2026-08-20
sess: sess-0820d
closed_at: 2026-08-20
closed_commit: b24dbbd7
closed_note: Обе половины доставлены и живут в проде. dada-argo: PYTHONPATH-фикс на origin/develop как b24dbbd7 (тот же патч, другой sha после ребейза параллельной сессии) — вердикт по СОДЕРЖИМОМУ, не по sha; живьём подтверждён на 8 подах: PYTHONPATH=/python-logging-setup:/app. jenkins-pipelines: d6dcafb на gh/develop, а прод-Jenkins грузит библиотеку dada-tuda-jenkins-pipelines с defaultVersion=develop (GlobalLibraries.xml из пода jenkins-6bfd4bf777-qdhvk). Локальный dada-argo develop остался позади пульта и несёт чужой WIP — не трогал. Остаточный вред вынесен в 0404: пострадавший gulyaev-ai-core лежит, потому что строка запуска запечена в старый образ.
---
Заземление [live 2026-08-20, git + kubectl + psql]:

Новая юзерша `lifecoachrussia@yandex.ru` (рега 08-19 09:37Z) деплоит первый апп `gulyaev-ai-core` в 11:26:50Z.
Он в CrashLoopBackOff 18ч+, ни разу не поднялся: контейнер выходит `Completed, exit 0` за ~4с с ПУСТЫМ логом.
Причина наша: `git_repos.start_command` пуст, автодетект дал `fastapi`, а пайплайн запускал точку входа
ФАЙЛОМ (`python app/main.py`) → `sys.path[0]=/app/app` → `ModuleNotFoundError: No module named 'app'`.
Наш сторож классифицировал верно (`app_health_alerts.cause_kind='app_entrypoint_import'`), письмо ушло за 8 минут.

Фикс состоит из ДВУХ половин, и доехала только одна:
1. ✅ `jenkins-pipelines` `d6dcafb` — ДОСТАВЛЕН, проверено `git merge-base --is-ancestor d6dcafb gh/develop` = YES (пульт `gh`, не `origin`): `PythonLaunch.groovy` даёт модульную форму
   `exec uvicorn app.main:app --host 0.0.0.0 --port 8000` под guard'ом наличия uvicorn, старый угадыватель
   остаётся фолбэком. Тест `test/PythonLaunchTest.groovy` 12/12, мутационный RED воспроизведён (4/12 падают).
2. 🔴 `dada-argo` `8bc92657` (`helm/common/templates/deployment.yaml`): `PYTHONPATH` был жёстко
   `/python-logging-setup`, что ВЫТЕСНЯЛО `/app` из пути импорта — независимый второй способ получить тот же
   `ModuleNotFoundError`. Коммит существует ТОЛЬКО в локальном `develop`, `git merge-base --is-ancestor` = NO против ОБОИХ пультов (`gh/develop` и `origin/develop`, один и тот же репозиторий); он среди 3 неотправленных (плюс `169acdad` про аргументы запуска CLI и `00da0554` про
   priorityClass нашего CI). В том же чекауте лежат НЕЗАКОММИЧЕННЫЕ правки того же файла от параллельной
   сессии, поэтому цикл 0820d не пушил: чужой WIP пушить нельзя, а разбирать чужое дерево — тем более.

Почему P0: половина фикса в чарте, не доехавшая до git, даёт ложное «доставлено» — ровно память
`project_backlog_close_by_commit_hides_undelivered_code`. Пока `8bc92657` не в `origin/develop`,
питоновские аппы с пакетной раскладкой продолжат падать даже с исправленной строкой запуска.

Второй факт, важный для всех, кто будет чинить пострадавших, — и он ДВУСТОРОННИЙ, не перепутайте половины:
- **Автодетектная команда запечена в образ** на этапе сборки. `redeployFrom`
  (`backend/internal/api/deployments.go:247`) переиспользует прежний `image_uri`, поэтому апп, сломанный
  НАШИМ автодетектом, вылечит только новая сборка — не редеплой и не откат.
- **Явная `start_command` — наоборот, значение чарта**: едет в `values.yaml` как `common.startCommand`
  (`gitops-agent/internal/renderer/renderer.go:343,478`) и применяется на рендере. Поэтому «задать команду
  запуска + редеплой» ЛЕЧИТ без пересборки, и именно на этом стоит починочный поток из `852ec3b4`.

Работа:
- Свериться с параллельной сессией/владельцем дерева `dada-argo`, довести `8bc92657` до `origin/develop`
  чистым пушем (без чужих незакоммиченных правок), дождаться синка Argo.
- Проверить на живом FastAPI-репо с раскладкой `app/main.py`, что новая сборка поднимается.
- Только после этого решать, что делать с аппом юзерши (её ресурс, сами не трогаем).

M2: `git merge-base --is-ancestor 8bc92657 gh/develop` = YES; свежесобранный тестовый FastAPI-апп
с пакетной раскладкой в песочнице 1/1 Running, а не exit 0.
