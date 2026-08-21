---
id: 0219
status: open
prio: P1
title: P1-ATTACHBOXDATABASE-НЕ-РЕАЛИЗОВАН-ДЛЯ-КЛАСТЕРА
created: 2026-08-06
sess: sess-0806j
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟠 P1-ATTACHBOXDATABASE-НЕ-РЕАЛИЗОВАН-ДЛЯ-КЛАСТЕРА (sess-0806j 2026-08-06, [code+live+psql]) — остаток после отгрузки `7219c36e` (тот закрыл только ЧЕСТНОСТЬ поверхности, не функцию). `initClusterBoxRuntime` (`backend/internal/api/box_runtime.go:187-233`) не присваивает `attach` → nil → `requireAttachProvider` (`box_runtime.go:375-393`) отдаёт 503 ВСЕГДА и ВСЕМ; единственный реализатор `LocalAttachProvider` (`backend/internal/box/localattach.go:53`) включается лишь при `BOX_LOCAL_ROOT`, которого в прод-ConfigMap нет. Воспроизведено живьём в `agent-sandbox` (одноразовый бокс, снят). За 7 дней в audit НИ ОДНОГО успеха `AttachBoxDatabase` ни у кого. Реальная работа = `ClusterAttachProvider` по пути из комментария `boxes_attach.go:16-26` (CreateServiceDatabase → ждать Committed → достать crossplane connection secret через `cloudtask.DBCredentialsResolver` → инжект env). Приоритет ниже воронки: боксы — не измеренное узкое место.
