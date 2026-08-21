---
id: 0151
status: open
prio: P1
title: INSTALLATION-ТОКЕН GITHUB ПЕЧАТАЕТСЯ В ЛОГ СБОРКИ ОТКРЫТЫМ ТЕКСТОМ
created: 2026-08-12
sess: sess-0812h
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟠 INSTALLATION-ТОКЕН GITHUB ПЕЧАТАЕТСЯ В ЛОГ СБОРКИ ОТКРЫТЫМ ТЕКСТОМ (sess-0812h, 2026-08-12, [live Jenkins dada-build #319 строка 157]) — `+ git clone --depth 1 --branch main https://x-access-token:ghs_3500292_<JWT>@github.com/...` видно всем, у кого есть доступ к Jenkins-логам. Токен короткоживущий (~1ч), но даёт доступ к репозиториям юзера. Чинить в build-agent: не эхоить команду клона (`set +x` вокруг git clone либо `git -c credential.helper` / env-подстановка).
