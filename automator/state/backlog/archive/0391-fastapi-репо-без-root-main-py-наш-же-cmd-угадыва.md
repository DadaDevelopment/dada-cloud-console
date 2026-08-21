---
id: 0391
status: closed
prio: P0
stream: 2
hypothesis: H02
title: FastAPI-репо без root main.py: наш же CMD угадывает python app/main.py и апп падает навсегда
created: 2026-08-20
sess: sess-0820d
closed_at: 2026-08-20
closed_commit: a3a023f4,d6dcafb,b24dbbd7
closed_note: M2 закрыт живым прогоном в agent-sandbox, не коммитом. Jenkins build #432 подгрузил shared library ровно d6dcafbdf0d7 (лог 'Checking out Revision d6dcafbdf'); отрендеренный CMD = uvicorn app.main:app --host 0.0.0.0 --port 8000 с прежним перебором как fallback; под fastapi-modulefix-probe-deploy-b947cccf4-cbgw4 в agent-sandbox-prod 1/1 Running ready=true, в логах 'Application startup complete', ModuleNotFoundError нет; PYTHONPATH в живом поде = /python-logging-setup:/app; /health изнутри кластера 200 {ok:true}. Зонд и его репозиторий удалены. Чужой gulyaev-ai-core не трогали — он поднимется только после пересборки самим владельцем.
---
Заземление [live 2026-08-20, psql + kubectl + build-лог #419]:

Новый юзер `lifecoachrussia@yandex.ru` зарегистрировался 2026-08-19 09:37:20Z,
за 73 секунды подключил GitHub, и через 16 часов его апп `gulyaev-ai-core`
лежит в CrashLoopBackOff с 198 рестартами. Он не заходил в консоль ни разу
после первых четырёх минут (ровно один `SessionStart` за всю историю).

Цепочка отказа — вся наша:

1. Четыре первых билда подряд умерли на НАШЕЙ инфре:
   `fail_reason=platform_error`, `trigger jenkins build: get crumb: crumb: 503`.
   Юзер нажал `TriggerAutofix` (09:41:19 — последнее человеческое действие)
   на платформенную ошибку, которую авто-фикс чинить не может.
2. Пятый билд (push, 11:25) собрался. Билд-агент ПРАВИЛЬНО определил фреймворк
   (`git_repos.framework_override=fastapi`, port 8000) и поставил uvicorn.
   Но сгенерированный CMD uvicorn не использует, а угадывает файл по имени:
   `for f in main.py bot.py app.py; do [ -f "$f" ] && exec python "$f"; done;`
   … `for d in */; do … [ -f "$d$f" ] && exec python "$d$f"`.
   В репо нет root-level `main.py`, поэтому запускается `python app/main.py`,
   Python ставит `sys.path[0]=/app/app`, `/app` не импортируется:
   `ModuleNotFoundError: No module named 'app'` (`kubectl logs --previous`).
3. Второй усилитель: мы инжектим `PYTHONPATH=/python-logging-setup` — единственный
   PYTHONPATH в контейнере, и он НЕ содержит `WORKDIR /app`. Будь там `/app`,
   даже неверная догадка `python app/main.py` импортировалась бы нормально.
4. Мы написали юзеру ОДНО письмо и обвинили в нём его же код:
   `app_health_alerts.cause='Судя по логам, это ошибка в коде приложения (Python)'`,
   `cause_kind='app_code'`, `last_send_ok=t`. Классификатор увёл
   `ModuleNotFoundError` в `pythonCrashSignatures`, потому что
   `needsArgsCrashSignatures` (`backend/internal/notify/notify.go:351`) знает
   только argparse/cobra/click.
5. Рычаг существует и недостижим: поле старт-команды есть
   (`backend/migrations/117_git_repos_start_command.sql`,
   `frontend/components/deploy/start-command-editor.tsx`), но ссылка на него
   в баннере рисуется ТОЛЬКО при `cause_kind === "app_needs_args"`
   (`frontend/components/deploy/app-alerts-banner.tsx:668`). При `app_code`
   юзер видит «ошибка в вашем коде» и кнопку «смотреть логи».

Почему P0 и почему это не единичный случай: `start_command` у этого репо пуст,
и класс воспроизводится на ЛЮБОМ python-репо, где точка входа лежит в пакете
(`app/main.py`, `src/main.py`) — то есть на стандартной раскладке FastAPI,
которую мы сами же и детектим. Мы определили фреймворк верно и всё равно
запустили его неверным способом.

Работа (три разных дефекта, порядок по цене):
- Детекция fastapi обязана рождать `uvicorn <module>:app --host 0.0.0.0 --port <port>`,
  а не угадывание имени файла.
- `PYTHONPATH` обязан содержать `/app`, а не только шим логирования.
- `ModuleNotFoundError` с именем модуля, совпадающим с директорией верхнего
  уровня репозитория, обязан классифицироваться так, чтобы баннер ПОКАЗАЛ
  рычаг старт-команды, и письмо не говорило «это ошибка в вашем коде».

M2: пересобранный `gulyaev-ai-core` поднимается 1/1 без ручной правки юзером;
на новом python-репо с `app/main.py` первый деплой живой; у крашнувшегося
`ModuleNotFoundError` в консоли видна ссылка на поле старт-команды.
