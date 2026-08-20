---
id: 0402
status: closed
prio: P1
stream: reliability
title: Страница настроек приложения падает на .map по nullable env_keys
created: 2026-08-20
sess: sess-0820d
closed_at: 2026-08-20
closed_commit: 6a0aafcf
closed_note: webhooks/env_keys помечены nullable, guard вынесен в frontend/lib/payments-connection.ts с тестами (node:test). npm run test:unit прогнан мной: 330/330. Источник null по-прежнему НЕ доказан — guard закрывает симптом. Доставка в прод НЕ подтверждена: фронт несёт a3a023f4.
---
8 событий error-boundary у 4 юзеров на странице настроек приложения
(frontend/app/(console)/projects/[projectId]/apps/[appName]/settings/page.tsx:15,:256) —
той самой, где правят env. Единственный env_keys.map во фронте:
frontend/components/payments/payments-manager.tsx:238 (рядом webhooks.map :226), оба без
guard; тип объявлен ненулевым в frontend/lib/api.ts:2115-2123.
Честно: источник null НЕ доказан — payments_connect.go:289-296,374-376 поле всегда
заполняет, payment_connections пуст. Крэши при этом реальные.
Фикс: guard на обоих .map, тип пометить env_keys?: string[] | null.
