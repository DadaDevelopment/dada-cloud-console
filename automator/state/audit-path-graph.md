# Путь юзера, граф переходов — перезапись 2026-09-07 (sess-0907a)

## Когорта новых (08-13..09-07, 14 внешних): активация 12/14 = 86% после выката фикса доставки
Upload-без-git работает: wgck и yzfy активировались ПОЛНОСТЬЮ через UploadSourceArchive (без единого git-действия).

## 3 неактивированных (точные терминальные действия)
1. **ivakinavv23@yandex.ru** (рег 09-03, signup_channel=yandex): upload jkjk → failure → TriggerAutofix x2 →
   400 «переподключите через GitHub App» (невыполнимо для upload-аппа) → тишина.
   = измеренная терминальная точка. ПОЧИНЕНО ed6bc0b0 (честный вердикт upload_app_no_git + скрытие кнопки).
2. **tarotreaderhimu@gmail.com** (08-21, git-путь): ConnectGitRepo(best-marriage-astrologer-in-guwahati) → 3 build failure →
   CreateServiceDatabase+SeedDatabaseDSN (пытался чинить БД!) → 3-й failure → ушёл. Никакого auto-fix-канала для git-юзера не сработало/не нашёл.
3. **saravananofficial13@gmail.com** (09-02): InstallSolution it-tools → CreatePublicApi x5 подряд 404 app_not_found
   (ввёл thunder.com / thunder.dpdns.org — не понял, что нужен СНАЧАЛА апп, PublicApi вешается на аппарат) → failure → ушёл.

## Граф переходов (свёртка когорты)
- SignUp → CreateProject(pending) → ViewProject → ViewApps: 14/14 (вход консоли здоров)
- ПЕРВОЕ содержательное действие: UploadSourceArchive (wgck, yzfy, ivakinavv23) | InstallSolution (saravanan) |
  ConnectGitRepo (tarotreaderhimu) | обзор/чаты (остальные)
- ТЕРМИНАЛЬНЫЕ (где сдались): TriggerAutofix-отказ (1), CreatePublicApi-404-стена (1), build-failure-спираль (1),
  «посмотрел и замолчал» (остальные неактивированных нет — все 12 дошли до CreateApp)
- BuildFinished(failure) → TriggerBuild retry: yzfy x2; TriggerAutofix после failure: ivakinavv23 x2 (оба отказали)

## Выводы в продукт
- ed6bc0b0 закрывает вывод №1 (autofix-стена для upload).
- Вывод №2 (backlog-кандидат): CreatePublicApi без аппа = 5 тупых 404 подряд; нужен inline-совет «сначала создайте приложение»
  или конверсия PublicApi-заявки в создание аппа. file: backend/internal/api/*publicapi* (проверить messages при заведении).
- Вывод №3: 3 подряд build-failure у git-юзера без единого успешного autofix-запуска = канал есть, но юзер не дошёл
  (artemmendeleev остаётся единственным в истории). Формулировка кнопки/цена клика — следующий рычаг (см. E75 примечание).
- Инструментирование: CreatePublicApi уже пишет reason=app_not_found в metadata — аудита достаточно.
