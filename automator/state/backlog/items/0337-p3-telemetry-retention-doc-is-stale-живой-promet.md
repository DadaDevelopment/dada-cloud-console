---
id: 0337
status: open
prio: P3
stream: 6
title: P3-TELEMETRY-RETENTION-DOC-IS-STALE: живой Prometheus держит **3 суток**, а project_telemetry_retention в памяти и доки говорят 15
sess: sess-0810e
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] P3-TELEMETRY-RETENTION-DOC-IS-STALE (sess-0810e): живой Prometheus держит **3 суток**, а `project_telemetry_retention` в памяти и доки говорят 15d/7d. Из-за этого «исторический пик потребления» как аргумент почти всегда опирается на окно, которого нет. Сверить с конфигом и переписать.
