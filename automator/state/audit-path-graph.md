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
