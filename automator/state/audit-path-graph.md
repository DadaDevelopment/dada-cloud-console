# Граф путей по audit_events (перезаписывается каждый разбор)

Окно разбора: 2026-09-07 06:00Z -> 2026-09-09 06:25Z [live psql pg-shard-0/cloud-console, sess-0909a].

## Новые юзеры окна: 2

### masaybasay@yandex.ru (09-07 12:19, канал yandex/alice: signup_source=alice.yandex.ru, channel=yandex) — АКТИВИРОВАН
 SignUp -> SessionStart -> CreateProject -> ViewProject -> ViewApps -> StartGitAppInstall -> FinishGitAppInstall (18с) -> ConnectGitRepo(maxx-coffee-bot) -> TriggerBuild -> ViewBuildLogs -> ViewApp -> BuildFinished(105с) -> CreateApp -> SetEnvVar -> DeployImageVersion (итерации SetEnvVar->Deploy x5 за 2ч) -> возвраты 13:05 и 13:41 (ViewApps->ViewApp).
- Полный путь до живого аппа за 4 минуты, затем три сессии настройки env. Первый канал yandex/alice с активацией - подтверждение E79 (yandex-IdP активируются).
- Болевая точка (наблюдение, не блокер): итерации SetEnvVar -> DeployImageVersion (5+ раз) - каждое изменение переменной = отдельный деплой. Потенциальный рычаг: batch env-edit без передеплоя каждого поля.

### dada-tuda.ru1@buss.gq (09-08 03:35, прямой пароль-рег, одноразовый домен buss.gq) — МЕРТВЫЙ СИГНАП
 SignUp -> SessionStart -> CreateProject -> ViewProject -> ViewApps -> SetAIRoutingMode -> CreateProject (дубль) -> тишина 27ч.
- Терминальное действие = CreateProject-дубль (повторное создание проекта через 70с после первого). Дважды создав проект и не увидев разницы, ушел. Кандидат: пустой экран после создания проекта (см. 0024 - утечка пустого экрана apps 2/2).
- buss.gq = одноразовый домен, вероятно бот/зеркалоферма - вес сигнала низкий.

## Переходы окна (топ)
 SetEnvVar -> DeployImageVersion 5
 DeployImageVersion -> SetEnvVar 3
 CreateProject -> ViewProject 2 | ViewProject -> ViewApps 2 | SignUp -> SessionStart 2
 FinishGitAppInstall -> ConnectGitRepo 1 | CreateApp -> SetEnvVar 1

## Живые юзеры окна
 artempro2021 (fanvk): BuildFinished + DeployImageVersion 09-08 19:28 - жив, сам передеплоился после 09-06 recovery.

## UX-выводы
1. 0491 остаётся актуален (фрикция форм невидима): buss.gq терминал = CreateProject-дубль; инструментирование форм создания (databases сделано d2728dfc) продолжить на project/app формах.
2. masay путь - второй случай активации с yandex-канала: evidence H05/E79 копится.
3. SetEnvVar->Deploy связка доминирует (8 переходов) - кандидат в следующий рычаг потока 2 (деплой-ц Ik цикла).

## Гигиена
 Панель 09-09: not_ready_other payments/ai-gateway-b72e67/api-zerkalo-ru = PublicApi-Pending с нет-ingress хостами (не юзерские аппы); ServiceDatabaseV2 zerkalo phase=Unknown last_synced 07-13 - мертвый хвост, не трогать (не наше решение).
