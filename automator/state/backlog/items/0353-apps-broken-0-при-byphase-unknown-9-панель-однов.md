---
id: 0353
status: open
prio: P2
stream: 6
title: apps.broken: 0 ПРИ by_phase.Unknown: 9 — панель одновременно утверждает «сломанного нет» и «девять аппов в неизвестной фазе»
created: 2026-08-11
sess: sess-0811d
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🟡 `apps.broken: 0` ПРИ `by_phase.Unknown: 9` (sess-0811d, 2026-08-11, [live api]) — панель одновременно утверждает «сломанного нет» и «девять аппов в неизвестной фазе». `brokenAppSnapshotPredicate` требует `live_source='k8s'`, а строки с NULL `live_source` (те самые git-заглушки) структурно невидимы обоим числам по-разному. Пока не измерено, что это за 9 строк — `unmeasured`, не «всё хорошо».
