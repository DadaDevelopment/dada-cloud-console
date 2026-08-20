---
id: 0273
status: open
prio: P3
stream: 2
title: A5-RESIDUAL sess-0731j · От M2-прогона остались СТРОКИ В БД без инфры (проект 352f7adc-0fdb-47c4-ad2c-600c4e76a826 m2-delwedge-6cc
sess: sess-0731j
section: 🎯 ПОТОК 2 — Deploy speed & reliability как продукт
---
- [ ] A5-RESIDUAL sess-0731j · От M2-прогона остались СТРОКИ В БД без инфры (проект `352f7adc-0fdb-47c4-ad2c-600c4e76a826` `m2-delwedge-6ccb0a`, апп `4248e8a0-...`). k8s и git вычищены и проверены. Удалять напрямую psql НЕ стал: у DeleteProject известны FK-каскады [память `project_deleteproject_fk_cascade`], снос строк руками = ровно тот класс, что уже рождал призрачные проекты. Снять при первом доступном DeleteApp.
