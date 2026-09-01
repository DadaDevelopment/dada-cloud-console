---
id: 0108
status: open
prio: P2
title: ПЕРВЫЙ ddc deploy ВЛАДЕЛЬЦА УПАЛ НА no_dockerfile, И ЭТО БЫЛ ГОЛЫЙ PYTHON
created: 2026-08-13
sess: sess-0814a
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟡 ПЕРВЫЙ `ddc deploy` ВЛАДЕЛЬЦА УПАЛ НА `no_dockerfile`, И ЭТО БЫЛ ГОЛЫЙ PYTHON (sess-0814a, 2026-08-13, [live psql `builds`]) — `tree` 10:34Z и `genagent` 13:52Z: `no_dockerfile: framework '' has no template and repo ships no Dockerfile`. Похоже уже закрыто коммитом `ecafd8de` («голый python не собирался вовсе»), последующие билды обоих аппов — `success`. ДЕЙСТВИЕ: подтвердить, что именно `ecafd8de` закрыл этот класс (прогнать голую папку с одним `.py` без `requirements.txt` через `ddc deploy` в песочнице), и если да — закрыть пункт; если нет — это верхняя утечка потока-1, чинить сразу.
