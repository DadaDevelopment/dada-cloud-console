---
id: 0187
status: open
prio: P2
title: UX CreateAppServer ОБЕЩАЕТ РЕГИОНЫ, КОТОРЫХ НЕТ
created: 2026-08-08
sess: sess-0808e
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟡 UX `CreateAppServer` ОБЕЩАЕТ РЕГИОНЫ, КОТОРЫХ НЕТ (sess-0808e, 2026-08-08, из разбора аудита) — бэкенд валидирует `ru1/ru2/kz1/eu1` (`backend/internal/api/appservers.go:161-163,247`), дропдаун предлагает то же (`frontend/app/(console)/projects/[projectId]/app-servers/page.tsx:348-351`), а terraform-провайдер умеет только `ru1`: заявка проходит валидацию и падает асинхронно `Region not found` (13 строк `PROCESSING_ERROR` за окно). Убрать неподдерживаемые из списка/дропдауна или завести флаг вместо статического списка.
