---
id: 0163
status: open
prio: P0
title: 4 записи failed_builds в панели — НАШИ, один корень
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 4 записи `failed_builds` в панели — НАШИ, один корень: `git_auth_failed: could not read Username for github.com` (`agent-orchestrator-ui`, `a2ahub-landing`, `dada-development-site` ×2, возраст 6-8 суток). Это не 4 поломки, а один протухший/отсутствующий git-креденшл на build-agent. Пока висит — панель показывает владельцу 4 красные строки, которые к юзерам отношения не имеют.
