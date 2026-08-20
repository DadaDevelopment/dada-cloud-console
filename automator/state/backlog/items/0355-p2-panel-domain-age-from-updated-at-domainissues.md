---
id: 0355
status: open
prio: P2
stream: 6
title: P2-PANEL-DOMAIN-AGE-FROM-UPDATED-AT · domain_issues.age_seconds считается от updated_at, а не от created_at, поэтому панель занижа
created: 2026-08-11
sess: sess-0811e
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🟡 P2-PANEL-DOMAIN-AGE-FROM-UPDATED-AT (sess-0811e, 2026-08-11, origin/main@11d84ada) · `domain_issues.age_seconds` считается от `updated_at`, а не от `created_at`, поэтому панель занижает возраст проблемы примерно втрое: `a2a-hub.pro` показывался как «30 часов» при реальном возрасте 8.7 суток [live api + psql]. Любая перезапись строки (ретрай выпуска серта, ресинк) обнуляет счётчик — то есть чем активнее мы дёргаем сломанный домен, тем моложе он выглядит, и самые застарелые поломки систематически уезжают вниз списка. Чинить возрастом от `created_at`; `updated_at` при желании оставить отдельным полем «последняя попытка».
