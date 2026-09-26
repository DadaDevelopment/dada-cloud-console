---
id: 0521
status: closed
prio: P0
stream: 1
hypothesis: H11
title: Upload без манифеста: framework="" даёт success-билд без установки зависимостей, апп сразу в CrashLoop
created: 2026-09-25
sess: sess-0925a
closed_at: 2026-09-26
closed_commit: 1e828dad
closed_note: Аплоад с неопознанным фреймворком больше не отдаёт зелёный билд: PythonArchivePlan собирает Dockerfile из всех requirements-*.txt при одной точке входа, иначе 400 с конкретной подсказкой до записи байтов и до постановки билда. Прогнано на живом postgres: 6/6 UploadSourceArchive RUN+PASS, sourcedetect 15/15.
---
[live psql 2026-09-25] artem4212@bk.ru, единственный новый юзер окна: рег 04:31:28,
UploadSourceArchive -> SetUploadPort(worker) -> BuildFinished success за 84с ->
CreateApp success 04:35. Билд 92153601-542b-4646-9dd2-750b59ee6f21:
status=success, archive_framework='' (ПУСТАЯ строка), archive_port=8080, fail_reason пуст.

Через 72с апп vishnevka в CrashLoopBackOff. [live psql app_health_alerts]
cause_kind='app_code', cause_line='ModuleNotFoundError: No module named "aiogram"'.
На 06:25 всё ещё падает, selfheal_rebuilt_at пуст.

Это НЕ ошибка юзера: детектор не опознал фреймворк (framework=''), значит шаг установки
зависимостей не выполнялся вообще, но билд всё равно объявлен success. Юзер видит
зелёный билд и созданный апп - то есть платформа сказала ему "готово" ровно в тот момент,
когда результат заведомо мёртв.

Аудит это НЕ ловит: терминальное действие юзера = CreateApp/success. Правда о продукте
живёт в resource_snapshots.phase и app_health_alerts, которые с audit_events не связаны.
Юзер не вернулся с 05:08 (последний ux_event).

Чинить: при framework='' билд не должен рапортовать success молча. Минимум - честный вердикт
на экране ("фреймворк не распознан, зависимости не установлены, вот как задать") ЛИБО детект
requirements.txt/package.json без манифеста фреймворка. Смотреть backend/internal/sourcedetect/
(там уже есть ветка static из 86d64424 - тот же класс задачи, решённый для html).
