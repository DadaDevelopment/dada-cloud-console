---
id: 0475
status: open
prio: P1
stream: 2
title: Юзер видит зелёный phase=Ready при http_status=502
created: 2026-08-22
sess: sess-0822f
---
Живой случай 2026-08-22: bruzas.85@mail.ru, апп sevarateambot. Снапшот несёт phase=Ready И http_status=502, но страница аппа рендерит только phase — владелец видит зелёное при мёртвом URL.

Улика: http_status/http_reason/url_status уже вычисляются в backend/internal/api/admin_overview.go:1112-1220 (админская панель их видит), но GET /api/v1/projects/{id}/environments/{env}/apps/{name} (backend/internal/api/apps.go:375-422) их не отдаёт.

Правка: протащить http_status/http_reason в ответ apps.go и показать вторым полюсом в статус-пилюле консоли.
