# Audit path graph (перезаписывается, не копится) — 2026-09-04 sess-0904a

Окно: регы с 2026-08-21 (9 шт). Источник: audit_events (actor_id join users) + ux_events + feedback.

## Новые юзеры: все 9 активировались (первый раз в истории замера)
| юзер | рег | событий | путь (сжато) |
|---|---|---|---|
| tarotreaderhimu | 08-21 | 19 | git-install -> app+DB -> builds OK |
| dl592675334 | 08-23 | 28 | git -> app+DB -> builds -> UpdateAppStartCommand (глубоко) |
| zqleaders | 08-25 | 19 | git -> app + AppServer (первое органич. CreateAppServer) -> builds |
| m206rv159 | 08-26 | 32 | git -> app+DB -> RevealDatabaseCredentials -> builds |
| messiajit4 | 08-28 | 41 | git -> app+DB -> SetEnvVar -> builds (самый глубокий путь) |
| kof97zip | 08-28 | 15 | image-deploy путь -> SetEnvVar, БЕЗ билдов |
| saravananofficial13 | 09-02 | 16 | git -> InstallSolution + CreatePublicApi (новый API-путь) |
| wgck | 09-03 | 15 | archive-upload -> DeployImageVersion -> builds (поток 1!) |
| ivakinavv23 | 09-03 | 18 | archive-upload -> TriggerAutofix (ПЕРВОЕ органич. автофикс-использование) -> builds |

## Переходы
- SignUp -> StartGitAppInstall/UploadSourceArchive: 9/9 (нулевая потеря до первого действия)
- Из 9: 7 git-install, 2 archive-upload; app создан 9/9 (исторический leak «рег -> 0 аппов» на этом окне исчез)
- Терминальных тупиков не видно: у каждого юзера есть BuildFinished/Deploy после настройки
- Farm-волн в когорте нет (после 08-08 чисто)

## UX-выводы
- 4 feedback-строки за окно = юзеры ДОХОДЯТ до доменов/портов и спотыкаются: 0492 (порт-валидация врёт диапазоном + слетает порт), 0493 (верификация домена проверяет run.place вместо fanclub.run.place). Оба на пути «сделать апп публичным» — следующий вероятный cliff.
- ivakinavv сам нажал TriggerAutofix при падении своего билда = H08 органически востребован; auto-fix flow заслуживает инструментирования события (кто/чем/исход), сейчас виден только TriggerAutofix без исхода.

## Инструментирование аудита (долг)
- audit_events не пишет исход автофикса (fixed? failed?) — добавить action AutoFixFinished c outcome.
