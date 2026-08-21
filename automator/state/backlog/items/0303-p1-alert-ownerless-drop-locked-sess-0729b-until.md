---
id: 0303
status: open
prio: P1
title: ~~P1-ALERT-OWNERLESS-DROP LOCKED sess-0729b until 2026-07-29T16:20Z~~
created: 2026-07-29
sess: sess-0729b
section: Chips из sess-0728a (инцидент fonbet disk-full)
---
- [~] ~~P1-ALERT-OWNERLESS-DROP LOCKED sess-0729b until 2026-07-29T16:20Z~~ (sess-0729a из E30, апгрейд P2→P1): проекты с owner_id=NULL молча дропают app-health алерты (client-a-prod: 3 crashloop-пода, лог `no owner email ... dropping alert`). НЕ орфан: Client A Corp = ЖИВОЙ активный юзер (sub 3ff561dc, 27 agent-chat msgs 07-28, провизил Postgres+S3 через write-tools — первый реальный E38-флоу; identity НЕ в users table = keycloak-only). Фикс: (а) owner-fallback (email из Keycloak по sub / org-owner), (б) разобраться почему identity без users-row и проект без owner_id (agent-chat-провизия? adoption-путь?). Тот же гэп унаследует volume-watcher. Юзеру письмо НЕ слал: активно итерирует, краши = его код (inference_pb2 / bad image tag).
