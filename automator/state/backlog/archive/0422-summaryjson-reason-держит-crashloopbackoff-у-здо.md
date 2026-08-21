---
id: 0422
status: closed
prio: P2
stream: 2
title: summary_json.reason держит CrashLoopBackOff у здорового аппа — врёт агентам и API
created: 2026-08-20
closed_at: 2026-08-20
closed_commit: 88430035
closed_note: reason/exit_code теперь едут тем же патчем, что status/ready/restarts; RED мутационный, GREEN на real-DB риге
---
Замер sess-0820m [live, через dada MCP `listApps`]. Апп `gulyaev-ai-core` (проект
lifecoachrussia@yandex.ru, новый юзер от 08-19) отдаёт одновременно: `phase: Ready`,
`status: Ready`, `ready: 1`, `restarts: 0`, `http_status: 200`, свежий `live_at` —
и при этом `reason: CrashLoopBackOff`.

То есть `reason` в `summary_json` — хвост прошлого состояния, который никто не гасит
при выздоровлении.

Почему это НЕ P0: чипы алертов в консоли на него не смотрят. Они читают cooldown-таблицы
`app_health_alerts`/`app_volume_alerts`/`app_url_alerts` со свежестью по
`COALESCE(last_seen_at, last_sent_at)` (`backend/internal/api/app_alerts.go`), так что
ложного красного юзер не видит.

Почему это всё-таки чинить: врёт оно всем, кто читает снапшот напрямую — MCP-тулы,
ассистент, наш собственный пульс. Прочитав такой снапшот, агент честно доложит «у живого
юзера краш-луп» — ровно та ошибка, ради которой заведена память «выздоровевший контейнер
оставался сломан навсегда».

Чинить: гасить/перезаписывать `reason` тем же стейтментом, что пишет `Ready` + `restarts: 0`,
а не оставлять прошлое значение.
