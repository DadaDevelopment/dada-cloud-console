---
id: 0203
status: open
prio: P2
title: КРАСНЫЙ STDERR ВО ВКЛАДКЕ ЛОГОВ — МЁРТВАЯ АФФОРДАНС — frontend/components/logs-viewer.tsx:~182 красит строку в красный при e.strea
created: 2026-08-07
sess: sess-0807p
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟡 КРАСНЫЙ STDERR ВО ВКЛАДКЕ ЛОГОВ — МЁРТВАЯ АФФОРДАНС (sess-0807p, 2026-08-07, [live OpenSearch]) — `frontend/components/logs-viewer.tsx:~182` красит строку в красный при `e.stream === "stderr"`, но в `filebeat-*` у падающих аппов **ноль** документов со `stream=stderr`: fluent-bit кладёт всё как stdout. То есть юзер, привыкший что ошибки красные, читает трейсбек тем же цветом, что и «Listening on 8080». Чинить либо на стороне сборщика (сохранять поток), либо в UI — подсвечивать по сигнатуре (`notify.ExtractCauseLine` уже умеет находить строку причины, логика переиспользуема).
