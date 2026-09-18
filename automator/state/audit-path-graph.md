# Audit path graph (перезаписывается каждый цикл разбора)

## Окно этого разбора: 2026-09-17 (sess-0917a), дельта с 09-16 + сквозной разбор artem

## Новые юзеры за окно
0 (live psql: users created_at > 2026-09-16 = NONE). Разбор = сквозной путь платёжно-мотивированного юзера.

## Случай цикла: artempro2021@bk.ru (fanvk, 3 приложения, три canceled-платежа) - ПЕРВЫЙ ДВОЙНОЙ РАЗРЫВ: наш фиксour-side баг закрыл наш же баг
Цепочка 09-10 12:25-12:31 [live psql ux_events, user 4b1b8d89]:
apps/fanvk (recovery.apps.view) -> apps (recovery.apps.view) -> **recovery.apps.click.payment_recurring_forbidden** 12:29:54 (from=/apps)
-> path=/projects/undefined/billing x4 (12:29:54-12:30:01) -> a:Обзор -> /projects/undefined -> a:Биллинг -> снова /projects/undefined/billing
-> тишина. Юзер ХОТЕЛ оплатить; CTA «Перейти к оплате» вёл на /projects/undefined/billing.

Причина [code, до фикса]: backend отдаёт payment-prompt БЕЗ project_id (дизайн: CreatePaymentFailed-аудит
пишется на org, platform_recovery.go:88-96), recoveryPromptHref строил `/projects/${prompt.project_id}/billing`
без проверки. Тип RecoveryPrompt в types.ts врал: project_id обязан.

Отгружено `7f59ca7f` (09-17): project_id/environment_id опциональны по контракту; href строится с fallback
на projectId из роута (компонент смонтирован в [projectId]/apps); нет ни того ни другого - CTA скрывается.
+2 теста. Эксперимент E135, measure 2026-10-01.

## Терминальные действия новых юзеров (сканирование окна 14д, live psql)
| исход | кто |
|---|---|
| ResolveAutofix success | wow83168 (nodejs-argo, 09-10) - из graph 09-16; PR-нотис-фикс b6a1d7a1 в проде, замер E134 10-07 |
| BuildFinished failure | y4ndex.danila (smirad), saravananofficial13, tarotreaderhimu (класс известен) |
| VerifyDomainAuthorization failure | staffybot (ACME pending, норм-стадия по ops-handoff 09-16) |
| TriggerAutofix failure | ivakinavv23 (jkjk) |
| InstallSolution failure | kkartov |
| CreateProject | dada-tuda.ru1@buss.gq |

## Панель/платформа
not_ready-строки панели - юзерские 503 (wow83168, gulyaev) и unmaintained-хвосты; платформенных причин нет (ops-handoff 09-16).

## НОВОЕ инструментирование, заведённое этим циклом
(1) **Битые внутренние URL как отдельный класс разрыва.** Паттерн `path LIKE '/projects/undefined%'` в
ux_events = юзер ушёл по битой внутренней ссылке. До сегодня нигде не считался. Проверка ретроспективой:
всего 4 события за всю историю (все - artem 09-10). Метрика должна оставаться 0; любое ненулевое значение =
P2-баг битой ссылки, искать источник по props.from. (2) **Revenue leak чекаута** - bl 0508 (H03):
инструментирование billing_checkout_started/abandoned, замер возврата artem после фикса.

## Вывод цикла
Разрыв №2 на пути artem к оплате устранён в коде (7f59ca7f); жди возврата, не строй нового: metрика H03
(первый ЧУЖОЙ succeeded-платёж) закрытых дыр больше не имеет по коду [code: recoveryPromptHref больше не
может вернуть /projects/undefined]. Следующий разрыв того же юзера = на стороне чекаута YooKassa (0508).

---

# 2026-09-18 sess-0918a — artem-путь: revenue-leak закрыт в измеримом слое; стена 09-25 следующая точка

## Путь artempro2021@bk.ru после фикса 7f59ca7f (3 сессии, live ux_events)
- 09-14 18:18: /callback → /projects → /apps (recovery.apps.view 18:18:41) → НЕ кликнул CTA → fanvk-апп → app_latest_build:success → меню → Выйти. Сессия 8с на решение по плашке.
- 09-15 14:14: /apps (recovery.apps.view) → fanvk → клик a:fanclub.run.place (свой живой сайт) → ушёл.
- 09-17 11:28: recovery.apps.view → тишина дальше (0 events).

## Выводы
1. Плашка recovery ВИДНА (view пишется 3/3 сессий), CTA не кликается ни разу - но и dismiss нет:
   юзер не отвергает предложение, он каждый раз уходит смотреть СВОЙ апп. Битых ссылок по-прежнему
   0 (метрика фикса 7f59ca7f держится).
2. Разрыв сместился: не «CTA ведёт в никуда», а «биллинг не в пути юзера». Насильно путь появится
   09-25 (квотная стена у всех активных орг, live billing_accounts) - там же теперь стоит полная
   инструментация клика/редиректа/ошибки (2171f58c) и Метрика-цель 637214589.
3. Контроль-метрика битых ссылок: ux_events /projects/undefined/billing = 0 за окно (фикс жив).
4. Инструментационный долг, закрытый здесь же: apps_row:lastmile_chip (565f1641) - E158 критерий-2
   больше не слеп.

## Граф переходов artem (3 сессии)
recovery.apps.view → ViewApp(fanvk) 3/3; ViewApp → клик своего домена 1/3; CTA→billing 0/3; dismiss 0/3.
