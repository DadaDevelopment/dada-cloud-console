---
id: 0364
status: open
prio: P0
stream: 6
title: СИРОТУ-ДОМЕН ПОСЛЕ DeleteApp НЕЛЬЗЯ УБРАТЬ НИКАКИМ ПУТЁМ ПРОДУКТА
created: 2026-08-13
sess: sess-0813n
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🔴 СИРОТУ-ДОМЕН ПОСЛЕ `DeleteApp` НЕЛЬЗЯ УБРАТЬ НИКАКИМ ПУТЁМ ПРОДУКТА (sess-0813n, 2026-08-13, [live API]) — воспроизвёл на себе в песочнице в этом же цикле: `DELETE .../apps/libfix-probe` снёс апп (снапшот 0 строк, поды 0), а `domain_hostnames` осталась `failed/app_deleted`; попытка `DELETE .../hostnames/{id}` → **409 `the default domain cannot be detached`**. Суррогатный домен запрещено отцеплять, пока апп жив — и продолжает быть запрещено, когда аппа уже нет. Отсюда и копятся 21 сирота в `domain_issues`. Правка: `DeleteApp` обязан удалять (а не «помечать failed») строку суррогатного домена в той же транзакции, что и апп — по тому же правилу, что `project_deleteapp_orphans_domain_row_under_live_app`; либо `DetachHostname` должен разрешать снятие, когда аппа нет.
