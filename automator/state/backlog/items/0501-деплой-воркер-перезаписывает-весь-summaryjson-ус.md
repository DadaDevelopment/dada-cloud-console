---
id: 0501
status: open
prio: P1
stream: 2
title: Деплой-воркер перезаписывает весь summary_json устаревшей копией: порт юзера слетает
created: 2026-09-12
---
Разбор 0492 (sess-0912a) разделил симптом на две половины.

ПОЛОВИНА 1 (range-ошибка на валидном порте) - УЖЕ ПОЧИНЕНА, не переоткрывать.
UpdateAppPort авто-снимает worker вместо отказа [code backend/internal/api/apps.go:1734,1737-1739], фронт claims range только на code==='invalid_port' [code frontend/components/deploy/port-editor.tsx:81-87]. Бэк и фронт везде 1..65535 байт-в-байт (apps.go:868-870, gitrepos.go:1459-1461 vs port-editor.tsx:66, common-config-editor.tsx:113, git/import/page.tsx:1069). Ни один компонент больше не мапит произвольный 400 в "порт вне диапазона". Коммит 3613dc49.

ПОЛОВИНА 2 (порт слетает после сохранения) - CONFIRMED механизм, это и есть этот пункт.
- UpdateAppPort пишет resource_snapshots.summary_json и НЕ бампает last_synced_at [code backend/internal/api/apps.go:1745-1748].
- gitops-agent deployImageVersion читает summary_json БЕЗ блокировки в начале [code gitops-agent/internal/worker/dbwatcher.go:2461-2469], держит копию в памяти через медленный render+git-commit [2494-2583], и в конце пишет ВЕСЬ объект через UpsertSnapshot [2585-2596].
- UpsertSnapshot = LWW по стенным часам и ПОЛНАЯ замена объекта, не merge [code gitops-agent/internal/db/snapshots.go:17-37, WHERE last_synced_at < EXCLUDED.last_synced_at, время = time.Now() на моменте записи].
Следствие: любая уже летящая deploy-операция того же аппа (первичный билд, смена env, start-command), захватившая СТАРЫЙ порт в память до правки юзера, финиширует ПОСЛЕ неё и молча затирает свежий порт - её timestamp всегда новее, потому что last_synced_at при правке не бампается, а объект пишется целиком.
Исключено: статус-реконсилятор UpdateLiveStatus - это jsonb || merge и он никогда не несёт ключ "port" [code gitops-agent/internal/worker/statusreconciler.go:1241-1273, snapshots.go:306-320], он клобберить не может.

Почему НЕ взято в sess-0912a: живых данных ровно 1 строка UpdateAppPort за всю историю платформы [live psql: 2026-08-25 00:06:35Z, artempro2021@bk.ru, app fanvk, outcome=success, port 8080, worker_cleared=true, redeploy_operation_id 46f0b954]. Ни одного повторного захода, ни одного failure. ux_events по настройке порта = 0 (все 68 строк с LIKE '%port%' - это Import/Support/exporter, ложное срабатывание подстроки). Анти-як гейт: пункт не двигает ИЗМЕРЕННОЕ узкое место.
Но класс дефекта опасный - это потеря пользовательской правки через full-object LWW, и он бьёт не только по порту, а по любому полю summary_json, которое юзер правит во время летящего деплоя.

Фикс (минимальный): в deployImageVersion не писать устаревшую копию - либо перечитать summary_json под FOR UPDATE непосредственно перед UpsertSnapshot, либо мержить только те поля, которыми владеет деплой (версия образа), а не заменять объект. Бамп last_synced_at в apps.go:1745-1748 сам по себе НЕ лечит: воркер пишет позже и всё равно выигрывает LWW.
Файлы: gitops-agent/internal/worker/dbwatcher.go:2461-2596, gitops-agent/internal/db/snapshots.go:17-37.
