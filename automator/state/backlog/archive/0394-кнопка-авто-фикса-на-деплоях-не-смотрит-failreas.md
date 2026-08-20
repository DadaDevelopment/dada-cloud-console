---
id: 0394
status: closed
prio: P1
stream: reliability
title: Кнопка авто-фикса на деплоях не смотрит fail_reason и жжёт AI на нашей же аварии
created: 2026-08-20
sess: sess-0820d
closed_at: 2026-08-20
closed_commit: 33e941af
closed_note: TriggerAutofix отказывает 422 platform_error_not_fixable, когда последняя упавшая сборка имеет fail_reason=platform_error; вторая кнопка на странице деплоев загейчена тем же isRepoFixable. Тесты прогнаны мной против рига: TestTriggerAutofix_RefusesWhenLatestFailedBuildIsPlatformError и TestLatestFailedBuildFailReason_ReturnsPersistedReason PASS; RED показан подменой условия — тест падает. Доставка в прод НЕ подтверждена: под несёт a3a023f4.
---
frontend/app/(console)/projects/[projectId]/apps/[appName]/deployments/page.tsx:404 гейтит
только canDeploy && b.status==='failed'. Соседняя карточка app-latest-build-card.tsx:312 честно
гейтит на isRepoFixable (build-failure.ts:58 исключает platform_error). Доказано ux_events:
2026-08-19 09:40:57 юзер посмотрел карточку с ДЕРЖАЩИМ гейтом и не кликнул, 09:41:18 кликнул
незагейченную кнопку на деплоях, 09:42:02 вышел. Гейт живёт только в одном React-компоненте,
поэтому API, вторая кнопка, MCP-тул triggerAutofix и агент-чат его обходят.
Настоящий фикс — backend/internal/api/autofix.go:63-140: TriggerAutofix не читает fail_reason
вообще, latestFailedBuildSummary (:420-445) скармливает модели 'Failure reason: platform_error'.
Побочно: 7 строк TriggerAutofix за всю историю, 0 ResolveAutofix — вердикт приехал только
0901f914, исторические прогоны навсегда без исхода.
