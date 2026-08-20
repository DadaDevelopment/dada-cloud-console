---
id: 0297
status: open
prio: P3
title: P2-SNAPSHOT-STALE-REASON · db.UpdateLiveStatus мержит summary_json = COALESCE(summary_json,'{}') || $2::jsonb
section: Открытые долги (не терять)
---
- [ ] P2-SNAPSHOT-STALE-REASON · `db.UpdateLiveStatus` мержит `summary_json = COALESCE(summary_json,'{}') || $2::jsonb` [code gitops-agent/internal/db/snapshots.go:311-313] — shallow-merge НИКОГДА не чистит ключи. `patchFields["reason"]` пишется только под `if la.reason != ""`, поэтому на тике где под транзиторно Running остаётся ПРОШЛЫЙ `"reason":"CrashLoopBackOff"` поверх свежего `phase=Pending`. Поймано живьём 1 раз (14:13:05Z, workassistantbot). Частота не измерена (n=1). Фикс: чистить crash-поля явно, а не полагаться на merge.
